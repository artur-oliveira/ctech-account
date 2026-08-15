package scopes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	ResourceCatalogPK  = "RESOURCE_SERVER"
	VisibilityPublic   = "public"
	VisibilityInternal = "internal"
	StatusActive       = "active"
	StatusDeprecated   = "deprecated"
	ManifestSchemaV1   = 1
	maxResourceScopes  = 100
	maxDescriptionLen  = 300
)

var (
	ErrResourceNotFound      = errors.New("resource server not found")
	ErrRevisionConflict      = errors.New("resource server revision conflict")
	ErrScopeRemoval          = errors.New("active scopes must be deprecated before removal")
	ErrInvalidResource       = errors.New("invalid resource server")
	ErrResourceAlreadyExists = errors.New("resource server already exists")
)

// ScopeDefinition is the v2 catalog representation owned by a resource
// server. Visibility controls discovery/consent; deprecated scopes remain
// resolvable for existing grants but cannot be granted to new clients.
type ScopeDefinition struct {
	Scope         string            `json:"name"                  dynamodbav:"scope"`
	Descriptions  map[string]string `json:"descriptions,omitempty" dynamodbav:"-"`
	Description   string            `json:"description,omitempty"  dynamodbav:"description"`
	DescriptionPT string            `json:"description_pt,omitempty" dynamodbav:"description_pt"`
	Visibility    string            `json:"visibility"            dynamodbav:"visibility"`
	Status        string            `json:"status"                dynamodbav:"status"`
}

// ResourceServer is the current v2 manifest plus immutable routing and
// ownership metadata provisioned by an operator.
type ResourceServer struct {
	PK                string            `json:"-"                    dynamodbav:"pk"`
	SK                string            `json:"resource_server_id"   dynamodbav:"sk"`
	DisplayName       string            `json:"display_name"         dynamodbav:"display_name"`
	Audience          string            `json:"audience"             dynamodbav:"audience"`
	PublisherClientID string            `json:"publisher_client_id"  dynamodbav:"publisher_client_id"`
	Scopes            []ScopeDefinition `json:"scopes"               dynamodbav:"scopes"`
	Revision          int64             `json:"revision"             dynamodbav:"revision"`
	ManifestHash      string            `json:"manifest_hash"        dynamodbav:"manifest_hash"`
	UpdatedAt         string            `json:"updated_at"           dynamodbav:"updated_at"`
	UpdatedBy         string            `json:"updated_by"           dynamodbav:"updated_by"`
	SourceRepository  string            `json:"source_repository"    dynamodbav:"source_repository,omitempty"`
	SourceRevision    string            `json:"source_revision"      dynamodbav:"source_revision,omitempty"`
}

func (r ResourceServer) ID() string { return r.SK }

// Manifest is the service-owned, environment-neutral contract accepted by
// the registry. Audience and publisher are deliberately operator-owned.
type Manifest struct {
	SchemaVersion    int               `json:"schema_version" validate:"required"`
	ResourceServerID string            `json:"resource_server_id" validate:"required,max=64"`
	DisplayName      string            `json:"display_name" validate:"required,max=120"`
	Scopes           []ScopeDefinition `json:"scopes" validate:"required,min=1,max=100"`
}

// ResourceRevision is an immutable snapshot written transactionally with the
// current resource row.
type ResourceRevision struct {
	PK               string            `dynamodbav:"pk"`
	SK               string            `dynamodbav:"sk"`
	ResourceServerID string            `dynamodbav:"resource_server_id"`
	Revision         int64             `dynamodbav:"revision"`
	PreviousHash     string            `dynamodbav:"previous_hash,omitempty"`
	ManifestHash     string            `dynamodbav:"manifest_hash"`
	DisplayName      string            `dynamodbav:"display_name"`
	Scopes           []ScopeDefinition `dynamodbav:"scopes"`
	UpdatedAt        string            `dynamodbav:"updated_at"`
	UpdatedBy        string            `dynamodbav:"updated_by"`
	SourceRepository string            `dynamodbav:"source_repository,omitempty"`
	SourceRevision   string            `dynamodbav:"source_revision,omitempty"`
}

func historyPK(id string) string      { return "RESOURCE_SERVER_HISTORY#" + id }
func historySK(revision int64) string { return fmt.Sprintf("REV#%020d", revision) }

// ValidateManifest validates syntax, namespace ownership and display text.
// Wildcards are rejected because the registry contains concrete grantable
// permissions, not client-side wildcard grants.
func ValidateManifest(m Manifest) error {
	if m.SchemaVersion != ManifestSchemaV1 {
		return fmt.Errorf("%w: schema_version must be %d", ErrInvalidResource, ManifestSchemaV1)
	}
	if !validResourceID(m.ResourceServerID) || m.ResourceServerID == IdentityService || m.ResourceServerID == InternalServicePrefix {
		return fmt.Errorf("%w: invalid or reserved resource_server_id", ErrInvalidResource)
	}
	if strings.TrimSpace(m.DisplayName) == "" || len(m.DisplayName) > 120 {
		return fmt.Errorf("%w: display_name is required and limited to 120 characters", ErrInvalidResource)
	}
	if len(m.Scopes) == 0 || len(m.Scopes) > maxResourceScopes {
		return fmt.Errorf("%w: scopes must contain 1-%d entries", ErrInvalidResource, maxResourceScopes)
	}
	seen := make(map[string]struct{}, len(m.Scopes))
	for _, entry := range m.Scopes {
		if !IsValid(entry.Scope) || strings.Contains(entry.Scope, "*") || IsOIDC(entry.Scope) {
			return fmt.Errorf("%w: invalid concrete scope %q", ErrInvalidResource, entry.Scope)
		}
		if _, ok := seen[entry.Scope]; ok {
			return fmt.Errorf("%w: duplicate scope %q", ErrInvalidResource, entry.Scope)
		}
		seen[entry.Scope] = struct{}{}
		description := entry.Description
		descriptionPT := entry.DescriptionPT
		if description == "" {
			description = entry.Descriptions["en"]
		}
		if descriptionPT == "" {
			descriptionPT = entry.Descriptions["pt-BR"]
		}
		if strings.TrimSpace(description) == "" || strings.TrimSpace(descriptionPT) == "" || len(description) > maxDescriptionLen || len(descriptionPT) > maxDescriptionLen {
			return fmt.Errorf("%w: scope %q requires English and pt-BR descriptions of at most %d characters", ErrInvalidResource, entry.Scope, maxDescriptionLen)
		}
		switch entry.Visibility {
		case VisibilityPublic:
			if !strings.HasPrefix(entry.Scope, m.ResourceServerID+":") {
				return fmt.Errorf("%w: public scope %q is outside namespace %q", ErrInvalidResource, entry.Scope, m.ResourceServerID)
			}
		case VisibilityInternal:
			if !strings.HasPrefix(entry.Scope, InternalServicePrefix+":"+m.ResourceServerID+":") {
				return fmt.Errorf("%w: internal scope %q is outside namespace %q", ErrInvalidResource, entry.Scope, m.ResourceServerID)
			}
		default:
			return fmt.Errorf("%w: scope %q visibility must be public or internal", ErrInvalidResource, entry.Scope)
		}
		if entry.Status != StatusActive && entry.Status != StatusDeprecated {
			return fmt.Errorf("%w: scope %q status must be active or deprecated", ErrInvalidResource, entry.Scope)
		}
	}
	return nil
}

func validResourceID(id string) bool {
	if id == "" || len(id) > 64 || id[0] < 'a' || id[0] > 'z' {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func ValidateAudience(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%w: audience must be an absolute HTTPS URL without query or fragment", ErrInvalidResource)
	}
	loopbackHTTP := u.Scheme == "http" && (u.Hostname() == "localhost" || isLoopbackIP(u.Hostname()))
	if u.Scheme != "https" && !loopbackHTTP {
		return fmt.Errorf("%w: audience must be an absolute HTTPS URL without query or fragment", ErrInvalidResource)
	}
	return nil
}

func isLoopbackIP(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func canonicalManifest(m Manifest) Manifest {
	m.DisplayName = strings.TrimSpace(m.DisplayName)
	m.Scopes = append([]ScopeDefinition(nil), m.Scopes...)
	for i := range m.Scopes {
		if m.Scopes[i].Description == "" {
			m.Scopes[i].Description = m.Scopes[i].Descriptions["en"]
		}
		if m.Scopes[i].DescriptionPT == "" {
			m.Scopes[i].DescriptionPT = m.Scopes[i].Descriptions["pt-BR"]
		}
		m.Scopes[i].Descriptions = nil
		m.Scopes[i].Description = strings.TrimSpace(m.Scopes[i].Description)
		m.Scopes[i].DescriptionPT = strings.TrimSpace(m.Scopes[i].DescriptionPT)
	}
	slices.SortFunc(m.Scopes, func(a, b ScopeDefinition) int { return strings.Compare(a.Scope, b.Scope) })
	return m
}

func ManifestHash(m Manifest) (string, error) {
	m = canonicalManifest(m)
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256-" + hex.EncodeToString(sum[:]), nil
}

func resourceFromManifest(current *ResourceServer, m Manifest, actor, sourceRepo, sourceRevision string) (*ResourceServer, error) {
	m = canonicalManifest(m)
	hash, err := ManifestHash(m)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return &ResourceServer{
		PK: ResourceCatalogPK, SK: current.SK, Audience: current.Audience,
		PublisherClientID: current.PublisherClientID, DisplayName: m.DisplayName,
		Scopes: m.Scopes, Revision: current.Revision + 1, ManifestHash: hash,
		UpdatedAt: now, UpdatedBy: actor, SourceRepository: sourceRepo,
		SourceRevision: sourceRevision,
	}, nil
}

func ensureNoActiveRemoval(current *ResourceServer, next Manifest) error {
	nextScopes := make(map[string]ScopeDefinition, len(next.Scopes))
	for _, entry := range next.Scopes {
		nextScopes[entry.Scope] = entry
	}
	for _, existing := range current.Scopes {
		if existing.Status != StatusActive {
			continue
		}
		entry, ok := nextScopes[existing.Scope]
		if !ok || entry.Status == StatusDeprecated {
			// Explicit active -> deprecated is allowed; omission is not.
			if ok {
				continue
			}
			return fmt.Errorf("%w: %s", ErrScopeRemoval, existing.Scope)
		}
	}
	return nil
}
