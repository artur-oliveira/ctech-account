package company

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/account/api/internal/database"
)

var (
	// ErrNotFound is a company or an actor edge that is not there.
	ErrNotFound = errors.New("not found")
	// ErrTaxIDTaken is the lock row losing its conditional write: this
	// organization already holds this tax id. Never a read-then-write check —
	// two people registering the same CNPJ at once must not both find nothing
	// and both write.
	ErrTaxIDTaken = errors.New("this organization already has this tax id")
)

const (
	companiesTable = "account_companies"
	lookupIndex    = "lookup-index"

	orgPKPrefix     = "ORG#"
	companySKPrefix = "COMPANY#"
	taxIDSKPrefix   = "TAXID#"
	actorSKPrefix   = "ACTOR#"
)

func orgPK(orgID string) string         { return orgPKPrefix + orgID }
func companySK(companyID string) string { return companySKPrefix + companyID }
func taxIDSK(canonical string) string   { return taxIDSKPrefix + canonical }

// actorSK nests the company id inside the sort key, so one Query with the
// prefix ACTOR#{company}# lists a company's actors and the table needs no
// second index for it.
func actorSK(companyID, userID string) string {
	return actorSKPrefix + companyID + "#" + userID
}

func lookupUserPK(userID string) string        { return "USER#" + userID }
func lookupSourcePK(system, ref string) string { return "SOURCE#" + system + "#" + ref }

func companyIDFromSK(sk string) string { return strings.TrimPrefix(sk, companySKPrefix) }

func actorIDsFromSK(sk string) (companyID, userID string) {
	companyID, userID, _ = strings.Cut(strings.TrimPrefix(sk, actorSKPrefix), "#")
	return companyID, userID
}

// Repository is the data access this domain needs. An interface so the service
// is testable without DynamoDB — the shape organization already uses.
type Repository interface {
	Create(ctx context.Context, c *Company, firstActor *Actor) error
	Get(ctx context.Context, orgID, companyID string) (*Company, error)
	List(ctx context.Context, orgID string) ([]*Company, error)
	GetBySourceRef(ctx context.Context, system, ref string) (*Company, error)
	UpdateNames(ctx context.Context, orgID, companyID, legal, trade string, now time.Time) error
	PutActor(ctx context.Context, a *Actor) error
	GetActor(ctx context.Context, orgID, companyID, userID string) (*Actor, error)
	ListActors(ctx context.Context, orgID, companyID string) ([]*Actor, error)
	ListForUser(ctx context.Context, userID string) ([]*Actor, error)
	RemoveActor(ctx context.Context, orgID, companyID, userID string) error
}

type repo struct {
	db        *dynamodb.Client
	companies database.Base
	tableName string
}

func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &repo{
		db:        db,
		companies: database.NewBase(db, tablePrefix, companiesTable),
		tableName: database.TableName(tablePrefix, companiesTable),
	}
}

// Create writes the company, its tax-id lock and its first actor in one
// transaction.
//
// The lock is what makes "one tax id per organization" a database invariant
// rather than a hope: a read-then-write would let two concurrent registrations
// of the same CNPJ both find nothing and both succeed.
func (r *repo) Create(ctx context.Context, c *Company, firstActor *Actor) error {
	item, err := attributevalue.MarshalMap(c)
	if err != nil {
		return fmt.Errorf("marshaling company: %w", err)
	}
	item["pk"] = &types.AttributeValueMemberS{Value: orgPK(c.OrganizationID)}
	item["sk"] = &types.AttributeValueMemberS{Value: companySK(c.ID)}
	// Sparse on purpose: only an imported company carries lookup_pk, so the
	// index holds exactly the rows a migration needs to recognize.
	if c.SourceSystem != "" && c.SourceRef != "" {
		item["lookup_pk"] = &types.AttributeValueMemberS{Value: lookupSourcePK(c.SourceSystem, c.SourceRef)}
	}

	lock := map[string]types.AttributeValue{
		"pk":         &types.AttributeValueMemberS{Value: orgPK(c.OrganizationID)},
		"sk":         &types.AttributeValueMemberS{Value: taxIDSK(c.TaxID)},
		"company_id": &types.AttributeValueMemberS{Value: c.ID},
	}

	writes := []types.TransactWriteItem{
		r.companies.BuildPutTxItemIfAbsent(item),
		r.companies.BuildPutTxItemIfAbsent(lock),
	}
	if firstActor != nil {
		actorItem, err := r.actorItem(firstActor)
		if err != nil {
			return err
		}
		writes = append(writes, r.companies.BuildPutTxItemIfAbsent(actorItem))
	}

	if err := r.companies.TransactWrite(ctx, writes); err != nil {
		if database.IsConditionFailed(err) {
			return ErrTaxIDTaken
		}
		return fmt.Errorf("creating company: %w", err)
	}
	return nil
}

func (r *repo) actorItem(a *Actor) (map[string]types.AttributeValue, error) {
	item, err := attributevalue.MarshalMap(a)
	if err != nil {
		return nil, fmt.Errorf("marshaling actor: %w", err)
	}
	item["pk"] = &types.AttributeValueMemberS{Value: orgPK(a.OrganizationID)}
	item["sk"] = &types.AttributeValueMemberS{Value: actorSK(a.CompanyID, a.UserID)}
	item["lookup_pk"] = &types.AttributeValueMemberS{Value: lookupUserPK(a.UserID)}
	return item, nil
}

func (r *repo) Get(ctx context.Context, orgID, companyID string) (*Company, error) {
	item, err := r.companies.GetItem(ctx, orgPK(orgID), companySK(companyID))
	if err != nil {
		return nil, fmt.Errorf("reading company: %w", err)
	}
	if item == nil {
		return nil, ErrNotFound
	}
	return unmarshalCompany(item)
}

func unmarshalCompany(item map[string]types.AttributeValue) (*Company, error) {
	var c Company
	if err := attributevalue.UnmarshalMap(item, &c); err != nil {
		return nil, fmt.Errorf("unmarshaling company: %w", err)
	}
	// The ids live in the key, not the item, so they have to be recovered here
	// or every company comes back not knowing who or where it is for.
	if pk, ok := item["pk"].(*types.AttributeValueMemberS); ok {
		c.OrganizationID = strings.TrimPrefix(pk.Value, orgPKPrefix)
	}
	if sk, ok := item["sk"].(*types.AttributeValueMemberS); ok {
		c.ID = companyIDFromSK(sk.Value)
	}
	return &c, nil
}

func (r *repo) List(ctx context.Context, orgID string) ([]*Company, error) {
	res, err := r.companies.Query(ctx, database.QueryOpts{
		PK: orgPK(orgID), SKPrefix: companySKPrefix, Limit: 500,
	})
	if err != nil {
		return nil, fmt.Errorf("listing companies: %w", err)
	}
	out := make([]*Company, 0, len(res.Items))
	for _, item := range res.Items {
		c, err := unmarshalCompany(item)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// GetBySourceRef answers "have I already imported this one". It reads a GSI, so
// the answer is eventually consistent — acceptable only because Create's lock
// row is conditional, which makes the worst case a rejected duplicate rather
// than two companies.
func (r *repo) GetBySourceRef(ctx context.Context, system, ref string) (*Company, error) {
	res, err := r.companies.QueryGSI(ctx, lookupIndex, "lookup_pk", lookupSourcePK(system, ref), 1, nil)
	if err != nil {
		return nil, fmt.Errorf("reading company by source ref: %w", err)
	}
	if len(res.Items) == 0 {
		return nil, ErrNotFound
	}
	return unmarshalCompany(res.Items[0])
}

// UpdateNames corrects the names. The tax id is absent deliberately: changing
// it would mean releasing one lock row and taking another, and a company whose
// tax id was wrong is a different company.
func (r *repo) UpdateNames(ctx context.Context, orgID, companyID, legal, trade string, now time.Time) error {
	ok, err := database.ConditionalUpdate(ctx, r.db, r.tableName,
		orgPK(orgID), aws.String(companySK(companyID)),
		map[string]any{"legal_name": legal, "trade_name": trade, "updated_at": now.UTC()},
		"attribute_exists(pk)", nil, nil)
	if err != nil {
		return fmt.Errorf("renaming company: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

func (r *repo) PutActor(ctx context.Context, a *Actor) error {
	item, err := r.actorItem(a)
	if err != nil {
		return err
	}
	if err := r.companies.PutItem(ctx, item); err != nil {
		return fmt.Errorf("granting actor: %w", err)
	}
	return nil
}

func (r *repo) GetActor(ctx context.Context, orgID, companyID, userID string) (*Actor, error) {
	item, err := r.companies.GetItem(ctx, orgPK(orgID), actorSK(companyID, userID))
	if err != nil {
		return nil, fmt.Errorf("reading actor: %w", err)
	}
	if item == nil {
		return nil, ErrNotFound
	}
	return unmarshalActor(item)
}

func unmarshalActor(item map[string]types.AttributeValue) (*Actor, error) {
	var a Actor
	if err := attributevalue.UnmarshalMap(item, &a); err != nil {
		return nil, fmt.Errorf("unmarshaling actor: %w", err)
	}
	if pk, ok := item["pk"].(*types.AttributeValueMemberS); ok {
		a.OrganizationID = strings.TrimPrefix(pk.Value, orgPKPrefix)
	}
	if sk, ok := item["sk"].(*types.AttributeValueMemberS); ok {
		a.CompanyID, a.UserID = actorIDsFromSK(sk.Value)
	}
	return &a, nil
}

func (r *repo) ListActors(ctx context.Context, orgID, companyID string) ([]*Actor, error) {
	res, err := r.companies.Query(ctx, database.QueryOpts{
		PK: orgPK(orgID), SKPrefix: actorSKPrefix + companyID + "#", Limit: 500,
	})
	if err != nil {
		return nil, fmt.Errorf("listing actors: %w", err)
	}
	return collectActors(res.Items)
}

// ListForUser answers "which companies may this person act for", across every
// organization. It reads the sparse lookup index, where only actor rows carry
// a USER# key — a company row is invisible to it, which is the point.
func (r *repo) ListForUser(ctx context.Context, userID string) ([]*Actor, error) {
	res, err := r.companies.QueryGSI(ctx, lookupIndex, "lookup_pk", lookupUserPK(userID), 500, nil)
	if err != nil {
		return nil, fmt.Errorf("listing companies for user: %w", err)
	}
	return collectActors(res.Items)
}

func collectActors(items []map[string]types.AttributeValue) ([]*Actor, error) {
	out := make([]*Actor, 0, len(items))
	for _, item := range items {
		a, err := unmarshalActor(item)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (r *repo) RemoveActor(ctx context.Context, orgID, companyID, userID string) error {
	ok, err := r.companies.DeleteItem(ctx, orgPK(orgID), actorSK(companyID, userID))
	if err != nil {
		return fmt.Errorf("revoking actor: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}
