package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"go.uber.org/fx"

	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/database"
	"gopkg.aoctech.app/account/api/internal/geoupdater"
	"gopkg.aoctech.app/account/api/internal/keystore"
	"gopkg.aoctech.app/api-commons/awsconfig"
	commoncache "gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/lock"
	"gopkg.aoctech.app/api-commons/supervisor"
	commonws "gopkg.aoctech.app/api-commons/ws"
)

// InfraModule is everything the domains stand on: workers, DynamoDB, Valkey,
// the support socket registry, the sealer and the signing keys.
var InfraModule = fx.Module("infra",
	fx.Provide(newWorkers, newDynamo, newValkey, newSupportWS, newSealer, newSigning),
	fx.Invoke(startKeyRotator, startGeoUpdater, registerWorkerShutdown),
)

// Workers runs background workers under the supervisor: cancellable context,
// panic restart, and (with Valkey) one leader per worker across instances.
type Workers struct {
	Ctx context.Context
	sup *supervisor.Supervisor
}

// Start runs a worker; a refusal means the supervisor already stopped.
func (w *Workers) Start(name string, fn func(context.Context) error) {
	if err := w.sup.Run(context.Background(), name, fn); err != nil {
		fatal("starting worker", "worker", name, "error", err)
	}
}

func newWorkers(lc fx.Lifecycle, cfg *config.Config) *Workers {
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	var locker *lock.Locker
	if cfg.ValkeyURL != "" {
		lockBackend, err := commoncache.NewRedisBackend(cfg.ValkeyURL)
		if err != nil {
			fatal("connecting worker lock backend", "error", err)
		}
		locker = lock.New(lockBackend, 30*time.Second)
	}
	// Stops last of all (OnStop runs in reverse order of construction).
	lc.Append(fx.Hook{OnStop: func(context.Context) error { cancelWorkers(); return nil }})
	return &Workers{
		Ctx: workerCtx,
		sup: supervisor.New(workerCtx, supervisor.Config{Locker: locker, LockPrefix: "supervisor:" + cfg.Environment + ":"}),
	}
}

// registerWorkerShutdown drains the workers after the server stops and before
// the Valkey client and the socket registry close: it is registered after both
// (it takes the registry), and OnStop runs in reverse.
func registerWorkerShutdown(lc fx.Lifecycle, w *Workers, _ commonws.Registry) {
	lc.Append(fx.Hook{OnStop: func(context.Context) error {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := w.sup.Shutdown(stopCtx); err != nil {
			slog.Error("worker shutdown error", "error", err)
		}
		return nil
	}})
}

func newDynamo(cfg *config.Config) *dynamodb.Client {
	db, err := database.New(context.Background(), cfg.AWSRegion)
	if err != nil {
		fatal("connecting to DynamoDB", "error", err)
	}
	return db
}

// valkeyRequired: Valkey is a hard production dependency (OAuth codes, MFA /
// passkey challenges, recovery tokens, rate limits; no DynamoDB fallback).
func valkeyRequired(cfg *config.Config) bool {
	return cfg.Environment != "dev" && cfg.Environment != "development"
}

func newValkey(lc fx.Lifecycle, cfg *config.Config, _ *Workers) *cache.Client {
	c, err := cache.New(cfg.ValkeyURL)
	if err != nil {
		fatal("connecting to Valkey", "error", err)
	}
	// Refuse to boot without it in non-dev environments so a misconfigured
	// instance never enters rotation (§1.1).
	if !c.Enabled() && valkeyRequired(cfg) {
		fatal("VALKEY_URL is required: OAuth codes, MFA tokens and rate limiting depend on Valkey", "environment", cfg.Environment)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { c.Close(); return nil }})
	return c
}

func newSupportWS(lc fx.Lifecycle, c *cache.Client) commonws.Registry {
	var reg commonws.Registry
	if raw := c.Raw(); raw != nil {
		reg = commonws.NewRedisRegistry(raw)
	} else {
		reg = commonws.NewMemoryRegistry()
	}
	if err := reg.Start(context.Background()); err != nil {
		fatal("starting support websocket registry", "error", err)
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { return reg.Stop(context.Background()) }})
	return reg
}

func newSealer(cfg *config.Config) *crypto.Sealer {
	s, err := crypto.NewSealer(cfg)
	if err != nil {
		fatal("creating sealer", "error", err)
	}
	return s
}

type signing struct {
	fx.Out
	JWT  *crypto.JWTService
	Keys *keystore.Store // nil in dev mode (single env key)
}

// newSigning: RSA_PRIVATE_KEY env = dev mode (single key, no rotation);
// otherwise versioned keys come from SSM and rotate automatically.
func newSigning(cfg *config.Config) signing {
	var (
		jwtSvc   *crypto.JWTService
		keyStore *keystore.Store
		err      error
	)
	if cfg.SigningKey != nil {
		jwtSvc, err = crypto.NewJWTService(cfg)
	} else {
		ctx := context.Background()
		awsCfg, awsErr := awsconfig.Load(ctx, cfg.AWSRegion)
		if awsErr != nil {
			fatal("loading AWS config for SSM", "error", awsErr)
		}
		keyStore = keystore.NewStore(ssm.NewFromConfig(awsCfg), cfg.Environment)
		active, previous, loadErr := keyStore.Load(ctx)
		if loadErr != nil {
			fatal("loading signing keys from SSM", "error", loadErr)
		}
		jwtSvc, err = crypto.NewJWTServiceWithKeys(cfg, active, previous)
	}
	if err != nil {
		fatal("initializing JWT service", "error", err)
	}
	return signing{JWT: jwtSvc, Keys: keyStore}
}

// startKeyRotator: hourly reload from SSM; rotate at 90 days under a Valkey
// lock so exactly one ASG instance generates the new key. Dev mode (env key)
// and Valkey-less deployments skip it; cmd/rotatekeys remains the manual path.
func startKeyRotator(cfg *config.Config, w *Workers, valkey *cache.Client, jwtSvc *crypto.JWTService, keyStore *keystore.Store) {
	if keyStore == nil || !valkey.Enabled() {
		return
	}
	// CAC-025: namespace the rotation lock by environment so a Valkey shared
	// across envs can't let one env block another.
	lockKey := keystore.LockKey
	if cfg.Environment != "" {
		lockKey = fmt.Sprintf("rotate_jwk_lock:%s", cfg.Environment)
	}
	// Every instance reloads keys, so no supervisor leader lock; the rotation
	// itself stays under its own per-rotation Valkey lock below.
	rotatorCfg := keystore.RotatorConfig{
		Store:  keyStore,
		Reload: jwtSvc.Reload,
		TryLock: func(ctx context.Context) (bool, error) {
			return valkey.SetNX(ctx, lockKey, "1", keystore.LockTTL)
		},
		Unlock: func(ctx context.Context) error {
			return valkey.Delete(ctx, lockKey)
		},
		Interval: keystore.CheckInterval,
		MaxAge:   keystore.KeyMaxAge,
		Now:      time.Now,
		Env:      cfg.Environment,
	}
	w.Start("keystore-rotator", func(ctx context.Context) error { keystore.RunRotator(ctx, rotatorCfg); return nil })
}

// startGeoUpdater: per-instance auto-updating local MaxMind database. Disabled
// (geo.Lookup always zero-value) when credentials aren't configured.
func startGeoUpdater(cfg *config.Config, w *Workers) {
	if cfg.MaxMindAccountID == "" || cfg.MaxMindLicenseKey == "" {
		slog.Info("MAXMIND_ACCOUNT_ID/MAXMIND_LICENSE_KEY not set — GeoIP lookups disabled")
		return
	}
	geoCfg := geoupdater.Config{
		DBPath:     cfg.MaxMindDBPath,
		AccountID:  cfg.MaxMindAccountID,
		LicenseKey: cfg.MaxMindLicenseKey,
		Interval:   geoupdater.DefaultInterval,
		StaleAfter: geoupdater.DefaultStaleAfter,
		Now:        time.Now,
	}
	geoupdater.Startup(context.Background(), geoCfg)
	w.Start("geoupdater", func(ctx context.Context) error { geoupdater.Run(ctx, geoCfg); return nil })
}
