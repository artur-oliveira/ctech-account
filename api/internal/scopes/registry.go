package scopes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.aoctech.app/account/api/internal/cache"
)

type ResourceRepository interface {
	LoadResources(ctx context.Context) ([]ResourceServer, error)
	GetResource(ctx context.Context, id string) (*ResourceServer, error)
	CreateResource(ctx context.Context, resource *ResourceServer) error
	ReconcileResource(ctx context.Context, previous *ResourceServer, next *ResourceServer) error
	GetRevision(ctx context.Context, id string, revision int64) (*ResourceRevision, error)
}

type RegistryService struct {
	repo  ResourceRepository
	cache *cache.Client
}

func NewRegistryService(repo ResourceRepository, cacheClient *cache.Client) *RegistryService {
	return &RegistryService{repo: repo, cache: cacheClient}
}

// BootstrapAccount installs the Account API's embedded manifest without an
// OAuth round trip. Account is both Authorization Server and Resource Server,
// so using a confidential publisher client here would create a circular first
// boot dependency. The reserved system publisher cannot be provisioned through
// the public/operator client commands.
func (s *RegistryService) BootstrapAccount(ctx context.Context, audience, sourceRevision string) (*ResourceServer, bool, error) {
	manifest, err := AccountManifest()
	if err != nil {
		return nil, false, err
	}
	if err := ValidateAudience(audience); err != nil {
		return nil, false, err
	}

	current, err := s.repo.GetResource(ctx, AccountResourceID)
	if errors.Is(err, ErrResourceNotFound) {
		now := time.Now().UTC().Format(time.RFC3339)
		initial := &ResourceServer{
			PK: ResourceCatalogPK, SK: AccountResourceID,
			DisplayName: manifest.DisplayName, Audience: audience,
			PublisherClientID: SystemAccountPublisher, Scopes: []ScopeDefinition{},
			Revision: 0, UpdatedAt: now, UpdatedBy: SystemAccountPublisher,
		}
		if createErr := s.repo.CreateResource(ctx, initial); createErr != nil && !errors.Is(createErr, ErrResourceAlreadyExists) {
			return nil, false, createErr
		}
		current, err = s.repo.GetResource(ctx, AccountResourceID)
	}
	if err != nil {
		return nil, false, err
	}
	if current.Audience != audience || current.PublisherClientID != SystemAccountPublisher {
		return nil, false, fmt.Errorf("%w: Account resource audience or system ownership does not match runtime configuration", ErrInvalidResource)
	}

	resource, changed, err := s.Publish(
		ctx,
		SystemAccountPublisher,
		manifest,
		current.Revision,
		"ctech-account",
		sourceRevision,
	)
	if errors.Is(err, ErrRevisionConflict) {
		// Concurrent ASG starts may race on the first reconciliation. Treat the
		// winner's identical manifest as success, but never hide a real conflict.
		latest, getErr := s.repo.GetResource(ctx, AccountResourceID)
		if getErr != nil {
			return nil, false, getErr
		}
		hash, hashErr := ManifestHash(manifest)
		if hashErr == nil && latest.ManifestHash == hash {
			return latest, false, nil
		}
	}
	return resource, changed, err
}

func (s *RegistryService) Get(ctx context.Context, id string) (*ResourceServer, error) {
	return s.repo.GetResource(ctx, id)
}

func (s *RegistryService) Provision(ctx context.Context, id, displayName, audience, publisherClientID string) (*ResourceServer, error) {
	if !validResourceID(id) || id == IdentityService || id == AccountResourceID || id == InternalServicePrefix {
		return nil, fmt.Errorf("%w: invalid or reserved resource_server_id", ErrInvalidResource)
	}
	if displayName == "" || publisherClientID == "" {
		return nil, fmt.Errorf("%w: display name and publisher client are required", ErrInvalidResource)
	}
	if err := ValidateAudience(audience); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	resource := &ResourceServer{PK: ResourceCatalogPK, SK: id, DisplayName: displayName, Audience: audience, PublisherClientID: publisherClientID, Scopes: []ScopeDefinition{}, Revision: 0, UpdatedAt: now, UpdatedBy: "operator"}
	if err := s.repo.CreateResource(ctx, resource); err != nil {
		return nil, err
	}
	return resource, nil
}

// Publish validates and atomically reconciles a manifest. The resource's
// audience and publisher binding are never accepted from the request body.
func (s *RegistryService) Publish(ctx context.Context, actor string, manifest Manifest, expectedRevision int64, sourceRepo, sourceRevision string) (*ResourceServer, bool, error) {
	if err := ValidateManifest(manifest); err != nil {
		return nil, false, err
	}
	current, err := s.repo.GetResource(ctx, manifest.ResourceServerID)
	if err != nil {
		return nil, false, err
	}
	if current.PublisherClientID != actor {
		return nil, false, fmt.Errorf("%w: publisher does not own resource server", ErrInvalidResource)
	}
	if current.Revision != expectedRevision {
		return nil, false, ErrRevisionConflict
	}
	if err := ensureNoActiveRemoval(current, manifest); err != nil {
		return nil, false, err
	}
	hash, err := ManifestHash(manifest)
	if err != nil {
		return nil, false, err
	}
	if current.ManifestHash == hash {
		if err := s.invalidate(ctx); err != nil {
			return nil, false, err
		}
		return current, false, nil
	}
	next, err := resourceFromManifest(current, manifest, actor, sourceRepo, sourceRevision)
	if err != nil {
		return nil, false, err
	}
	if err := s.repo.ReconcileResource(ctx, current, next); err != nil {
		return nil, false, err
	}
	if err := s.invalidate(ctx); err != nil {
		return nil, true, err
	}
	return next, true, nil
}

func (s *RegistryService) Restore(ctx context.Context, id string, revision, expectedRevision int64, actor string) (*ResourceServer, error) {
	current, err := s.repo.GetResource(ctx, id)
	if err != nil {
		return nil, err
	}
	if current.Revision != expectedRevision {
		return nil, ErrRevisionConflict
	}
	snapshot, err := s.repo.GetRevision(ctx, id, revision)
	if err != nil {
		return nil, err
	}
	manifest := Manifest{SchemaVersion: ManifestSchemaV1, ResourceServerID: id, DisplayName: snapshot.DisplayName, Scopes: snapshot.Scopes}
	// Operator restore intentionally bypasses the no-removal rule: the complete
	// previous revision is the recovery source of truth.
	next, err := resourceFromManifest(current, manifest, actor, "operator-restore", fmt.Sprintf("revision-%d", revision))
	if err != nil {
		return nil, err
	}
	if err := s.repo.ReconcileResource(ctx, current, next); err != nil {
		return nil, err
	}
	if err := s.invalidate(ctx); err != nil {
		return nil, err
	}
	return next, nil
}

func (s *RegistryService) invalidate(ctx context.Context) error {
	if s.cache == nil || !s.cache.Enabled() {
		return nil
	}
	if err := s.cache.Delete(ctx, CatalogCacheKey); err != nil {
		return fmt.Errorf("invalidating scope catalog cache: %w", err)
	}
	return nil
}

func IsRegistryValidationError(err error) bool {
	return errors.Is(err, ErrInvalidResource) || errors.Is(err, ErrScopeRemoval)
}
