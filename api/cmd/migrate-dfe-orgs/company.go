package main

import (
	"context"
	"fmt"
	"strings"
	"time"
	"uuid"

	companyDomain "gopkg.aoctech.app/account/api/internal/domain/company"
)

// companyDecision is what this migration proposes to do with the Company half
// of one dfe organization.
//
// The organization half moved first (ctech-billing ADR 0021). This is the pass
// that unfuses them: a dfe organization was always a company — its primary key
// is the CNPJ — and ADR 0022 gave that half a home of its own.
type companyDecision struct {
	SourceRef string
	TaxID     string
	TaxIDKind string
	LegalName string
	CreatedAt time.Time
	Action    string
	Review    []string
	Notes     []string
	// Actors is everybody who may act for this company, owner included. Unlike
	// the organization's Members, the owner is NOT excluded: the company's
	// first actor is written by Create and the rest by PutActor, and the plan
	// carries all of them so the report shows the whole roster.
	Actors []companyDomain.Actor
}

// planCompany decides what happens to one organization's company half.
//
// The tax id comes from the legacy partition key, and that is the one place in
// this whole re-key where reading a key for a document is correct: the legacy
// key IS the document, which is exactly the property ADR 0022 removes.
func planCompany(org dfeOrg, members []dfeMember, orgDecision decision) companyDecision {
	d := companyDecision{
		SourceRef: org.PK,
		LegalName: strings.TrimSpace(org.Name),
		CreatedAt: orgDecision.CreatedAt,
		Action:    actionCreate,
	}

	raw := legacyDocument(org.PK)
	if raw == "" {
		d.Review = append(d.Review, fmt.Sprintf("the key %q is not a CNPJ_/CPF_ key, so it carries no document", org.PK))
	} else if canonical, kind, ok := companyDomain.NormalizeTaxID(raw); ok {
		d.TaxID, d.TaxIDKind = canonical, kind
	} else {
		// A key that never was a valid document. It emitted under it anyway, so
		// this is not ours to correct: a human decides whether the company is
		// real and what its tax id should be.
		d.Review = append(d.Review, fmt.Sprintf("%q is not a valid CNPJ or CPF", raw))
	}

	if d.LegalName == "" {
		// The company's legal name is what appears on a document's emit node
		// here and on an invoice in billing. Importing an empty one moves the
		// problem instead of surfacing it.
		d.Review = append(d.Review, "the organization has no name to use as a legal name")
	}

	for _, m := range members {
		userID := strings.TrimSpace(m.UserID)
		if userID == "" {
			// Already reported by planOrg for the same row; repeating it here
			// would double every such line in the report.
			continue
		}
		created, err := parseDFETime(m.CreatedAt)
		if err != nil {
			created = d.CreatedAt
		}
		d.Actors = append(d.Actors, companyDomain.Actor{
			UserID:    userID,
			Name:      strings.TrimSpace(m.Name),
			GrantedBy: strings.TrimSpace(m.InvitedBy),
			CreatedAt: created,
		})
	}

	// The owner acts for their own company even with no membership row — dfe
	// let that happen, and planOrg already notes it.
	if orgDecision.OwnerUserID != "" && !hasActor(d.Actors, orgDecision.OwnerUserID) {
		d.Actors = append(d.Actors, companyDomain.Actor{
			UserID:    orgDecision.OwnerUserID,
			Name:      orgDecision.OwnerName,
			GrantedBy: orgDecision.OwnerUserID,
			CreatedAt: d.CreatedAt,
		})
	}

	if len(d.Actors) == 0 {
		// A company nobody may act for cannot emit, and nothing in the product
		// offers a way to grant the first edge. It would need a support ticket.
		d.Review = append(d.Review, "no member and no owner; nobody would be able to act for this company")
	}

	if len(d.Review) > 0 {
		d.Action = actionReview
	}
	return d
}

func hasActor(actors []companyDomain.Actor, userID string) bool {
	for _, a := range actors {
		if a.UserID == userID {
			return true
		}
	}
	return false
}

// legacyDocument returns the document a legacy dfe key carries, or "" when the
// key is not one.
//
// It exists here and nowhere else on purpose. Every other place that used to
// read a document out of a key was a bug this re-key removes; this one reads a
// key that is being retired, which is the only correct use left.
func legacyDocument(pk string) string {
	for _, prefix := range []string{"CNPJ_", "CPF_"} {
		if after, ok := strings.CutPrefix(pk, prefix); ok {
			return after
		}
	}
	return ""
}

type companyApplyResult struct {
	CompanyID      string
	CreatedCompany bool
	CreatedActors  int
}

// applyCompany writes one planned company under an organization that already
// exists.
//
// Idempotent and resumable in the same shape as apply: the source ref finds a
// company already imported, and each actor edge is written whether or not the
// company was created on this run — so a first run that died between the
// company and its third actor is completed by the next, not skipped.
func applyCompany(
	ctx context.Context,
	repo companyDomain.Repository,
	organizationID string,
	d companyDecision,
) (companyApplyResult, error) {
	var res companyApplyResult

	existing, err := repo.GetBySourceRef(ctx, sourceSystem, d.SourceRef)
	switch {
	case err == nil:
		res.CompanyID = existing.ID
	case isCompanyNotFound(err):
		now := d.CreatedAt
		c := &companyDomain.Company{
			OrganizationID: organizationID,
			ID:             uuid.NewV7().String(),
			TaxID:          d.TaxID,
			TaxIDKind:      d.TaxIDKind,
			LegalName:      d.LegalName,
			SourceSystem:   sourceSystem,
			SourceRef:      d.SourceRef,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		// No first actor here: they are all written in the loop below, so a
		// re-run that finds the company already there still fills in whoever is
		// missing. Passing one would write it twice on the first run.
		if err := repo.Create(ctx, c, nil); err != nil {
			return res, fmt.Errorf("creating company for %s: %w", d.SourceRef, err)
		}
		res.CompanyID = c.ID
		res.CreatedCompany = true
	default:
		return res, fmt.Errorf("looking up company %s: %w", d.SourceRef, err)
	}

	for _, a := range d.Actors {
		a.OrganizationID = organizationID
		a.CompanyID = res.CompanyID
		if err := repo.PutActor(ctx, &a); err != nil {
			return res, fmt.Errorf("granting %s on company %s: %w", a.UserID, res.CompanyID, err)
		}
		res.CreatedActors++
	}
	return res, nil
}
