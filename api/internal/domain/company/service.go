package company

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"uuid"
)

var (
	// ErrInvalidTaxID is a tax id that is not a well-formed CNPJ or CPF. It
	// says nothing about whether the document exists — that is not knowable
	// here, and pretending otherwise would reject a CNPJ issued this morning.
	ErrInvalidTaxID = errors.New("tax id is not a valid CNPJ or CPF")
	// ErrInvalidName is an empty or overlong legal name.
	ErrInvalidName = errors.New("legal name is required")
)

// Service holds the rules a conditional write cannot express on its own.
//
// It does not re-check the caller's organization role: RequireOrgRole already
// resolved it for this request, and a second read of the same membership row is
// a second thing to keep in agreement with the first. What it does enforce is
// what that middleware cannot see — the tax id, the names, and the actor edge.
type Service struct {
	repo Repository
	now  func() time.Time
}

func NewService(repo Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, now: now}
}

// Register adds a company to an organization and makes the registrant its first
// actor.
//
// The edge is granted in the same transaction because a company nobody may act
// for is a company that needs a second request to fix — and a support ticket
// when that request is the one that fails.
func (s *Service) Register(ctx context.Context, orgID, actorUserID, actorName, rawTaxID, legalName, tradeName string) (*Company, error) {
	canonical, kind, ok := NormalizeTaxID(rawTaxID)
	if !ok {
		return nil, ErrInvalidTaxID
	}
	legal, trade, ok := ValidateNames(legalName, tradeName)
	if !ok {
		return nil, ErrInvalidName
	}
	now := s.now().UTC()
	c := &Company{
		OrganizationID: orgID,
		ID:             uuid.NewV7().String(),
		TaxID:          canonical,
		TaxIDKind:      kind,
		LegalName:      legal,
		TradeName:      trade,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	first := &Actor{
		OrganizationID: orgID,
		CompanyID:      c.ID,
		UserID:         actorUserID,
		Name:           strings.TrimSpace(actorName),
		GrantedBy:      actorUserID,
		CreatedAt:      now,
	}
	if err := s.repo.Create(ctx, c, first); err != nil {
		return nil, err
	}
	return c, nil
}

// List returns an organization's companies, ordered by the name people read.
func (s *Service) List(ctx context.Context, orgID string) ([]*Company, error) {
	companies, err := s.repo.List(ctx, orgID)
	if err != nil {
		return nil, err
	}
	sort.Slice(companies, func(i, j int) bool {
		return strings.ToLower(companies[i].LegalName) < strings.ToLower(companies[j].LegalName)
	})
	return companies, nil
}

func (s *Service) Get(ctx context.Context, orgID, companyID string) (*Company, error) {
	return s.repo.Get(ctx, orgID, companyID)
}

// Rename corrects the names. The tax id is not renameable: correcting it would
// mean releasing one lock row and taking another, and a company whose tax id
// was wrong is a different company — register that one and stop using this.
func (s *Service) Rename(ctx context.Context, orgID, companyID, legalName, tradeName string) error {
	legal, trade, ok := ValidateNames(legalName, tradeName)
	if !ok {
		return ErrInvalidName
	}
	return s.repo.UpdateNames(ctx, orgID, companyID, legal, trade, s.now().UTC())
}

// GrantActor lets somebody act for a company.
//
// The company is read first so a grant naming an id from another organization —
// or nothing at all — fails instead of writing a row nothing can reach.
func (s *Service) GrantActor(ctx context.Context, orgID, companyID, targetUserID, targetName, grantedBy string) error {
	if _, err := s.repo.Get(ctx, orgID, companyID); err != nil {
		return err
	}
	return s.repo.PutActor(ctx, &Actor{
		OrganizationID: orgID,
		CompanyID:      companyID,
		UserID:         targetUserID,
		Name:           strings.TrimSpace(targetName),
		GrantedBy:      grantedBy,
		CreatedAt:      s.now().UTC(),
	})
}

func (s *Service) RevokeActor(ctx context.Context, orgID, companyID, targetUserID string) error {
	return s.repo.RemoveActor(ctx, orgID, companyID, targetUserID)
}

func (s *Service) ListActors(ctx context.Context, orgID, companyID string) ([]*Actor, error) {
	actors, err := s.repo.ListActors(ctx, orgID, companyID)
	if err != nil {
		return nil, err
	}
	sort.Slice(actors, func(i, j int) bool {
		return strings.ToLower(actors[i].Name) < strings.ToLower(actors[j].Name)
	})
	return actors, nil
}

// MayAct is the question ctech-dfe asks before it lets somebody issue.
//
// A missing edge is false with no error: "not permitted" is an answer, not a
// failure, and a caller forced to tell them apart will treat one as the other
// on the first refactor.
func (s *Service) MayAct(ctx context.Context, orgID, companyID, userID string) (bool, error) {
	_, err := s.repo.GetActor(ctx, orgID, companyID, userID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ReachOf answers "may this person act for this company", without being told
// which organization the company belongs to.
//
// That asymmetry is the point. It is the question another product asks
// (ctech-billing ADR 0023), and the caller does not know the organization —
// finding it out is half of what it wants. The answer carries it back, so the
// product can store both ids and keep its own authorization lookup to one read.
//
// It reads the person's own edges rather than the company's, which is bounded
// by how many companies one person acts for. The other direction would need the
// organization to build the key, which is exactly what the caller lacks.
//
// An unknown company and a company this person cannot reach answer identically:
// ("", false, nil). Distinguishing them would make this route a probe for which
// company ids are real, and "not permitted" is an answer rather than a failure.
func (s *Service) ReachOf(ctx context.Context, companyID, userID string) (string, bool, error) {
	edges, err := s.repo.ListForUser(ctx, userID)
	if err != nil {
		return "", false, err
	}
	for _, e := range edges {
		if e.CompanyID == companyID {
			return e.OrganizationID, true, nil
		}
	}
	return "", false, nil
}
