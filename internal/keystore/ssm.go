package keystore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

const (
	activeParamFmt   = "/ctech-account/%s/jwk/active"
	previousParamFmt = "/ctech-account/%s/jwk/previous"
	// legacyParamFmt is the pre-rotation single-key parameter, wrapped once by
	// InitFromLegacy.
	legacyParamFmt = "/ctech-account/%s/rsa-private-key"
)

// SSMAPI is the subset of *ssm.Client the store needs (mockable in tests).
type SSMAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	PutParameter(ctx context.Context, in *ssm.PutParameterInput, opts ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
}

// Store reads and writes the versioned signing keys in SSM.
type Store struct {
	client SSMAPI
	env    string
}

func NewStore(client SSMAPI, environment string) *Store {
	return &Store{client: client, env: environment}
}

func (s *Store) activePath() string   { return fmt.Sprintf(activeParamFmt, s.env) }
func (s *Store) previousPath() string { return fmt.Sprintf(previousParamFmt, s.env) }

func (s *Store) getKey(ctx context.Context, name string) (*Key, error) {
	out, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return nil, err
	}
	var j KeyJSON
	if err := json.Unmarshal([]byte(aws.ToString(out.Parameter.Value)), &j); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", name, err)
	}
	return ParseKey(j)
}

// Load reads the active and previous keys. A missing previous parameter is
// not an error (nil); a missing or unreadable active key is fatal.
func (s *Store) Load(ctx context.Context) (*Key, *Key, error) {
	active, err := s.getKey(ctx, s.activePath())
	if err != nil {
		return nil, nil, fmt.Errorf("loading active jwk: %w", err)
	}
	previous, err := s.getKey(ctx, s.previousPath())
	if err != nil {
		var nf *types.ParameterNotFound
		if errors.As(err, &nf) {
			return active, nil, nil
		}
		return nil, nil, fmt.Errorf("loading previous jwk: %w", err)
	}
	return active, previous, nil
}

func (s *Store) putKey(ctx context.Context, name string, k *Key) error {
	j, err := k.ToJSON()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("encoding key %s: %w", k.KID, err)
	}
	_, err = s.client.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(name),
		Value:     aws.String(string(raw)),
		Type:      types.ParameterTypeSecureString,
		Overwrite: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	return nil
}

// Save persists the key pair, previous FIRST: a crash between the two writes
// leaves the old active key untouched and still valid.
func (s *Store) Save(ctx context.Context, active, previous *Key) error {
	if previous != nil {
		if err := s.putKey(ctx, s.previousPath(), previous); err != nil {
			return err
		}
	}
	return s.putKey(ctx, s.activePath(), active)
}

// Rotate generates a new active key and demotes the current active to
// previous. Returns the new key.
func Rotate(ctx context.Context, store *Store, now time.Time) (*Key, error) {
	active, _, err := store.Load(ctx)
	if err != nil {
		return nil, err
	}
	newKey, err := Generate(now)
	if err != nil {
		return nil, err
	}
	if err := store.Save(ctx, newKey, active); err != nil {
		return nil, err
	}
	return newKey, nil
}

// InitFromLegacy wraps the pre-rotation rsa-private-key parameter into
// jwk/active, preserving its derived KID so already-issued tokens keep
// verifying. Refuses to run when jwk/active already exists.
func InitFromLegacy(ctx context.Context, store *Store, client SSMAPI, now time.Time) error {
	if _, err := store.getKey(ctx, store.activePath()); err == nil {
		return fmt.Errorf("%s already exists — refusing to overwrite", store.activePath())
	}

	legacyPath := fmt.Sprintf(legacyParamFmt, store.env)
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(legacyPath),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("reading legacy key %s: %w", legacyPath, err)
	}

	key, err := parseLegacyPEM(aws.ToString(out.Parameter.Value), now)
	if err != nil {
		return err
	}
	return store.Save(ctx, key, nil)
}
