// Package geoupdater keeps the local MaxMind GeoLite2 City database
// (internal/geo's reader) fresh. Each EC2 instance in the ASG downloads and
// refreshes its own independent copy — there is no shared store for this
// file (unlike internal/keystore's SSM-backed signing keys), so no
// distributed lock is needed; redundant per-instance downloads against
// MaxMind's own endpoint are expected and within normal license use.
package geoupdater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschwald/geoip2-golang"
	"gopkg.aoctech.app/account/api/internal/geo"
)

const (
	// DefaultInterval is how often the background loop checks staleness.
	DefaultInterval = 24 * time.Hour
	// DefaultStaleAfter is the file age past which a refresh is attempted.
	// GeoLite2 ships weekly; 7 days keeps a fresh copy without hammering
	// MaxMind's endpoint.
	DefaultStaleAfter = 7 * 24 * time.Hour
	// maxStartupJitter spreads a fleet-wide deploy's first tick over up to an
	// hour so every ASG instance doesn't hit MaxMind at the same moment.
	maxStartupJitter = time.Hour

	realDownloadURL = "https://download.maxmind.com/geoip/databases/GeoLite2-City/download?suffix=tar.gz"
)

// Config wires the updater to its collaborators.
type Config struct {
	DBPath     string
	AccountID  string
	LicenseKey string
	Interval   time.Duration
	StaleAfter time.Duration
	Now        func() time.Time
	HTTPClient *http.Client
	// downloadURL overrides realDownloadURL in tests. Zero value in
	// production, which resolveDownloadURL turns into the real endpoint.
	downloadURL string
}

func (cfg Config) resolveDownloadURL() string {
	if cfg.downloadURL != "" {
		return cfg.downloadURL
	}
	return realDownloadURL
}

func (cfg Config) httpClient() *http.Client {
	if cfg.HTTPClient != nil {
		return cfg.HTTPClient
	}
	return http.DefaultClient
}

// Startup ensures internal/geo has a reader loaded before the server starts
// accepting traffic where possible, without ever blocking boot indefinitely.
// If DBPath already has a file, it's opened as-is (even if stale — the
// background Run loop refreshes it on its next tick). If not, one blocking
// download attempt is made; on failure, geo lookups stay disabled (zero
// Location) rather than crash-looping the instance over a non-critical
// feature.
func Startup(ctx context.Context, cfg Config) {
	if info, err := os.Stat(cfg.DBPath); err == nil && info.Size() > 0 {
		r, err := geoip2.Open(cfg.DBPath)
		if err != nil {
			slog.Warn("geoupdater: existing db file is unreadable, geo lookups disabled until next refresh", "path", cfg.DBPath, "error", err)
			return
		}
		geo.SetReader(r)
		return
	}
	if err := update(ctx, cfg); err != nil {
		slog.Warn("geoupdater: initial download failed, geo lookups disabled", "error", err)
	}
}

// Run refreshes the database every cfg.Interval once it's older than
// cfg.StaleAfter, jittering the first tick by up to an hour. Blocks until ctx
// is cancelled — run in a goroutine, same convention as keystore.RunRotator.
func Run(ctx context.Context, cfg Config) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(rand.Int63n(int64(maxStartupJitter)))):
	}

	t := time.NewTicker(cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := maybeUpdate(ctx, cfg); err != nil {
				slog.Error("geoupdater: update tick failed", "error", err)
			}
		}
	}
}

// maybeUpdate downloads a fresh database only if the local file is missing
// or older than cfg.StaleAfter.
func maybeUpdate(ctx context.Context, cfg Config) error {
	info, err := os.Stat(cfg.DBPath)
	stale := err != nil || cfg.Now().Sub(info.ModTime()) > cfg.StaleAfter
	if !stale {
		return nil
	}
	return update(ctx, cfg)
}

// update downloads the database tar.gz, extracts the .mmdb entry, validates
// it opens successfully, atomically replaces DBPath, and swaps the live
// reader. A failure at any step leaves the existing file and reader
// untouched.
func update(ctx context.Context, cfg Config) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.resolveDownloadURL(), nil)
	if err != nil {
		return fmt.Errorf("building download request: %w", err)
	}
	req.SetBasicAuth(cfg.AccountID, cfg.LicenseKey)

	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("downloading database: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading database: unexpected status %d", resp.StatusCode)
	}

	destDir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("creating database directory: %w", err)
	}

	tmpPath, err := extractMMDB(resp.Body, destDir)
	if err != nil {
		return fmt.Errorf("extracting database: %w", err)
	}

	r, err := geoip2.Open(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("validating downloaded database: %w", err)
	}

	if err := os.Rename(tmpPath, cfg.DBPath); err != nil {
		_ = r.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("installing downloaded database: %w", err)
	}

	geo.SetReader(r)
	return nil
}

// extractMMDB reads a gzipped tar stream and writes the first entry whose
// name ends in ".mmdb" to a temp file in destDir (same filesystem as the
// eventual DBPath, so the caller's os.Rename is atomic). Returns the temp
// file's path.
func extractMMDB(body io.Reader, destDir string) (string, error) {
	gz, err := gzip.NewReader(body)
	if err != nil {
		return "", fmt.Errorf("opening gzip stream: %w", err)
	}
	defer func(gz *gzip.Reader) {
		err := gz.Close()
		if err != nil {

		}
	}(gz)

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", fmt.Errorf("no .mmdb entry found in archive")
		}
		if err != nil {
			return "", fmt.Errorf("reading tar entry: %w", err)
		}
		if !strings.HasSuffix(hdr.Name, ".mmdb") {
			continue
		}

		tmp, err := os.CreateTemp(destDir, "geolite2-city-*.mmdb.tmp")
		if err != nil {
			return "", fmt.Errorf("creating temp file: %w", err)
		}
		if _, err := io.Copy(tmp, tr); err != nil {
			err := tmp.Close()
			if err != nil {
				return "", err
			}
			_ = os.Remove(tmp.Name())
			return "", fmt.Errorf("writing temp file: %w", err)
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmp.Name())
			return "", fmt.Errorf("closing temp file: %w", err)
		}
		return tmp.Name(), nil
	}
}
