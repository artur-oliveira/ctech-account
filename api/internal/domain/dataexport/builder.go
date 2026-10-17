package dataexport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/apikey"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	"gopkg.aoctech.app/account/api/internal/domain/session"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

// Section is one file of the account part. Each loader returns an explicit
// whitelist of fields, never a stored model: secrets (password hash, key
// hashes, session tokens, KYC object keys) must not reach the zip.
type Section struct {
	Name string // file name without extension
	Desc string // one line for the README
	Load func(ctx context.Context, userID string) (any, error)
}

// ZipBuilder renders sections as one JSON file each plus a README.txt (E2).
type ZipBuilder struct {
	sections []Section
	now      func() time.Time
}

func NewZipBuilder(sections ...Section) *ZipBuilder {
	return &ZipBuilder{sections: sections, now: time.Now}
}

func (b *ZipBuilder) Build(ctx context.Context, userID string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	readme := "CTech Account — a copy of your personal data\nGenerated at " + b.now().UTC().Format(time.RFC3339) + "\n\nFiles:\n"
	for _, s := range b.sections {
		v, err := s.Load(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("dataexport: section %s: %w", s.Name, err)
		}
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return nil, err
		}
		w, err := zw.Create(s.Name + ".json")
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
		readme += "  " + s.Name + ".json — " + s.Desc + "\n"
	}
	readme += "\nNot included: passwords and their hashes, API key secrets, session tokens and identity documents' files.\n"
	w, err := zw.Create("README.txt")
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte(readme)); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Sources are the account services the sections read.
type Sources struct {
	Users interface {
		GetByID(ctx context.Context, id string) (*user.User, error)
	}
	Sessions interface {
		List(ctx context.Context, userID string) ([]*session.Session, error)
	}
	Keys interface {
		List(ctx context.Context, userID string) ([]*apikey.APIKey, error)
	}
	Consents interface {
		List(ctx context.Context, userID string) ([]*consent.Grant, error)
	}
	Audit interface {
		ListByUser(ctx context.Context, userID, cursor string, limit int32) ([]*audit.Event, string, error)
	}
}

// maxActivityPages bounds the activity read (50 per page); the audit trail
// expires on its own TTL, so this is a ceiling, not a cut of recent history.
const maxActivityPages = 100

// AccountSections are the sections of ctech-account's own part.
func AccountSections(src Sources) []Section {
	return []Section{
		{Name: "profile", Desc: "your registration and identity data", Load: func(ctx context.Context, id string) (any, error) {
			u, err := src.Users.GetByID(ctx, id)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"email": u.Email, "email_verified": u.EmailVerified,
				"first_name": u.FirstName, "last_name": u.LastName, "display_name": u.DisplayName,
				"legal_name": u.LegalName, "cpf": u.CPF, "birth_date": u.BirthDate,
				"phone_number": u.PhoneNumber, "address": u.Address,
				"kyc_level": u.KYCLevel, "kyc_status": u.KYCStatus,
				"kyc_documents": u.KYCDocuments, // id, type and date only: the file key is json:"-"
				"tos_version":   u.TOSVersion, "tos_accepted_at": u.TOSAcceptedAt,
				"privacy_version": u.PrivacyVersion, "privacy_accepted_at": u.PrivacyAcceptedAt,
				"created_at": u.CreatedAt,
			}, nil
		}},
		{Name: "sessions", Desc: "devices signed in to your account", Load: func(ctx context.Context, id string) (any, error) {
			list, err := src.Sessions.List(ctx, id)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(list))
			for _, s := range list {
				out = append(out, map[string]any{
					"device": s.DeviceName, "ip_address": s.IPAddress, "user_agent": s.UserAgent,
					"city": s.GeoCity, "region": s.GeoRegion, "country": s.GeoCountry,
					"created_at": s.CreatedAt, "last_used_at": s.LastUsedAt,
				})
			}
			return out, nil
		}},
		{Name: "api-keys", Desc: "your API keys (name, prefix and permissions; never the secret)", Load: func(ctx context.Context, id string) (any, error) {
			list, err := src.Keys.List(ctx, id)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(list))
			for _, k := range list {
				out = append(out, map[string]any{"name": k.Name, "prefix": k.KeyPrefix, "scopes": k.Scopes,
					"created_at": k.CreatedAt, "last_used_at": k.LastUsedAt})
			}
			return out, nil
		}},
		{Name: "consents", Desc: "the apps you authorized and what each can access", Load: func(ctx context.Context, id string) (any, error) {
			list, err := src.Consents.List(ctx, id)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(list))
			for _, g := range list {
				out = append(out, map[string]any{"client_id": g.ClientID(), "scopes": g.Scopes,
					"granted_at": g.CreatedAt, "updated_at": g.UpdatedAt})
			}
			return out, nil
		}},
		{Name: "activity", Desc: "security events of your account", Load: func(ctx context.Context, id string) (any, error) {
			var out []map[string]any
			cursor := ""
			for range maxActivityPages {
				evs, next, err := src.Audit.ListByUser(ctx, id, cursor, 50)
				if err != nil {
					return nil, err
				}
				for _, e := range evs {
					out = append(out, map[string]any{"event": e.EventType, "at": e.CreatedAt, "ip_address": e.IP,
						"user_agent": e.UserAgent, "details": e.Metadata})
				}
				if next == "" {
					break
				}
				cursor = next
			}
			return out, nil
		}},
	}
}
