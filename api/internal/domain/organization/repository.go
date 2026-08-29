package organization

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
	// ErrNotFound is returned for an organization, a membership or an
	// invitation that is not there. One error for all three: the caller of
	// GetMembership already knows which it asked for, and a separate error per
	// aggregate is three things to map in the handler that say the same thing.
	ErrNotFound = errors.New("not found")
	// ErrAlreadyMember is a conditional-write failure, never a read-then-write
	// check: two invitations accepted at once must not both succeed.
	ErrAlreadyMember = errors.New("already a member")
)

const (
	orgsTable        = "account_organizations"
	membershipsTable = "account_memberships"
	invitationsTable = "account_invitations"
	lookupIndex      = "lookup-index"
	metaSK           = "META"

	memberSKPrefix = "MEMBER#"
	inviteSKPrefix = "INVITE#"
	orgPKPrefix    = "ORG#"
)

func orgPK(id string) string            { return orgPKPrefix + id }
func memberSK(userID string) string     { return memberSKPrefix + userID }
func lookupUserPK(userID string) string { return "USER#" + userID }

// inviteSK normalizes the address, so inviting the same person twice replaces
// the pending invitation instead of creating a second one nobody can see.
func inviteSK(email string) string {
	return inviteSKPrefix + NormalizeEmail(email)
}

// NormalizeEmail is exported because the service compares the address on an
// accepted invitation against the address on the session, and both sides have
// to normalize the same way or the comparison rejects the right person.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func orgIDFromPK(pk string) string  { return strings.TrimPrefix(pk, orgPKPrefix) }
func userIDFromSK(sk string) string { return strings.TrimPrefix(sk, memberSKPrefix) }
func emailFromSK(sk string) string  { return strings.TrimPrefix(sk, inviteSKPrefix) }

// Repository is the data access this domain needs. An interface so the service
// is testable without DynamoDB — the same shape support and kyc already use.
type Repository interface {
	CreateWithOwner(ctx context.Context, org *Organization) error
	Get(ctx context.Context, id string) (*Organization, error)
	UpdateDisplayName(ctx context.Context, id, name string, now time.Time) error
	GetMembership(ctx context.Context, orgID, userID string) (*Membership, error)
	ListMembers(ctx context.Context, orgID string) ([]*Membership, error)
	ListForUser(ctx context.Context, userID string) ([]*Membership, error)
	PutMembership(ctx context.Context, m *Membership) error
	SetRole(ctx context.Context, orgID, userID, role string) error
	RemoveMembership(ctx context.Context, orgID, userID string) error
	TransferOwnership(ctx context.Context, orgID, fromUserID, toUserID string, now time.Time) error
	PutInvitation(ctx context.Context, inv *Invitation) error
	GetInvitationByToken(ctx context.Context, tokenHash string) (*Invitation, error)
	ListInvitations(ctx context.Context, orgID string) ([]*Invitation, error)
	DeleteInvitation(ctx context.Context, orgID, email string) error
}

type repo struct {
	db              *dynamodb.Client
	orgs            database.Base
	memberships     database.Base
	invitations     database.Base
	orgsName        string
	membershipsName string
}

func NewRepository(db *dynamodb.Client, tablePrefix string) Repository {
	return &repo{
		db:              db,
		orgs:            database.NewBase(db, tablePrefix, orgsTable),
		memberships:     database.NewBase(db, tablePrefix, membershipsTable),
		invitations:     database.NewBase(db, tablePrefix, invitationsTable),
		orgsName:        database.TableName(tablePrefix, orgsTable),
		membershipsName: database.TableName(tablePrefix, membershipsTable),
	}
}

func (r *repo) membershipItem(m *Membership) (map[string]types.AttributeValue, error) {
	item, err := attributevalue.MarshalMap(m)
	if err != nil {
		return nil, fmt.Errorf("marshaling membership: %w", err)
	}
	item["pk"] = &types.AttributeValueMemberS{Value: orgPK(m.OrganizationID)}
	item["sk"] = &types.AttributeValueMemberS{Value: memberSK(m.UserID)}
	item["lookup_pk"] = &types.AttributeValueMemberS{Value: lookupUserPK(m.UserID)}
	return item, nil
}

// CreateWithOwner writes the organization and its single owner membership in
// one transaction. Two writes would leave a window in which an organization
// exists with nobody able to reach it, and a failure in that window is silent.
func (r *repo) CreateWithOwner(ctx context.Context, org *Organization) error {
	orgItem, err := attributevalue.MarshalMap(org)
	if err != nil {
		return fmt.Errorf("marshaling organization: %w", err)
	}
	orgItem["pk"] = &types.AttributeValueMemberS{Value: orgPK(org.ID)}
	orgItem["sk"] = &types.AttributeValueMemberS{Value: metaSK}

	memberItem, err := r.membershipItem(&Membership{
		OrganizationID: org.ID,
		UserID:         org.OwnerUserID,
		Role:           RoleOwner,
		CreatedAt:      org.CreatedAt,
	})
	if err != nil {
		return err
	}

	err = r.orgs.TransactWrite(ctx, []types.TransactWriteItem{
		r.orgs.BuildPutTxItemIfAbsent(orgItem),
		r.memberships.BuildPutTxItemIfAbsent(memberItem),
	})
	if database.IsConditionFailed(err) {
		return ErrAlreadyMember
	}
	return err
}

func (r *repo) Get(ctx context.Context, id string) (*Organization, error) {
	item, err := r.orgs.GetItem(ctx, orgPK(id), metaSK)
	if err != nil {
		return nil, fmt.Errorf("reading organization: %w", err)
	}
	if item == nil {
		return nil, ErrNotFound
	}
	var org Organization
	if err := attributevalue.UnmarshalMap(item, &org); err != nil {
		return nil, fmt.Errorf("unmarshaling organization: %w", err)
	}
	org.ID = id
	return &org, nil
}

func (r *repo) UpdateDisplayName(ctx context.Context, id, name string, now time.Time) error {
	ok, err := database.ConditionalUpdate(ctx, r.db, r.orgsName, orgPK(id), aws.String(metaSK),
		map[string]any{"display_name": name, "updated_at": now.UTC()},
		"attribute_exists(pk)", nil, nil)
	if err != nil {
		return fmt.Errorf("renaming organization: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

func (r *repo) GetMembership(ctx context.Context, orgID, userID string) (*Membership, error) {
	item, err := r.memberships.GetItem(ctx, orgPK(orgID), memberSK(userID))
	if err != nil {
		return nil, fmt.Errorf("reading membership: %w", err)
	}
	if item == nil {
		return nil, ErrNotFound
	}
	return unmarshalMembership(item)
}

func unmarshalMembership(item map[string]types.AttributeValue) (*Membership, error) {
	var m Membership
	if err := attributevalue.UnmarshalMap(item, &m); err != nil {
		return nil, fmt.Errorf("unmarshaling membership: %w", err)
	}
	// The ids live in the key, not the item, so they have to be recovered here
	// or every membership comes back not knowing who or where it is for.
	if pk, ok := item["pk"].(*types.AttributeValueMemberS); ok {
		m.OrganizationID = orgIDFromPK(pk.Value)
	}
	if sk, ok := item["sk"].(*types.AttributeValueMemberS); ok {
		m.UserID = userIDFromSK(sk.Value)
	}
	return &m, nil
}

func (r *repo) ListMembers(ctx context.Context, orgID string) ([]*Membership, error) {
	res, err := r.memberships.Query(ctx, database.QueryOpts{
		PK: orgPK(orgID), SKPrefix: memberSKPrefix, Limit: 500,
	})
	if err != nil {
		return nil, fmt.Errorf("listing members: %w", err)
	}
	return collectMemberships(res.Items)
}

// ListForUser answers "which organizations is this person in", which is the
// query the console makes on every sign-in. It reads the sparse lookup index,
// so a row without lookup_pk is invisible to it — that is the point: only
// membership rows carry one.
func (r *repo) ListForUser(ctx context.Context, userID string) ([]*Membership, error) {
	res, err := r.memberships.QueryGSI(ctx, lookupIndex, "lookup_pk", lookupUserPK(userID), 200, nil)
	if err != nil {
		return nil, fmt.Errorf("listing organizations for user: %w", err)
	}
	return collectMemberships(res.Items)
}

func collectMemberships(items []map[string]types.AttributeValue) ([]*Membership, error) {
	out := make([]*Membership, 0, len(items))
	for _, item := range items {
		m, err := unmarshalMembership(item)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// PutMembership refuses to overwrite an existing membership. Accepting an
// invitation twice, or two invitations at once, must not silently reset a role
// somebody already has.
func (r *repo) PutMembership(ctx context.Context, m *Membership) error {
	item, err := r.membershipItem(m)
	if err != nil {
		return err
	}
	err = r.memberships.TransactWrite(ctx, []types.TransactWriteItem{
		r.memberships.BuildPutTxItemIfAbsent(item),
	})
	if database.IsConditionFailed(err) {
		return ErrAlreadyMember
	}
	if err != nil {
		return fmt.Errorf("writing membership: %w", err)
	}
	return nil
}

// SetRole refuses to touch a row that is not there and refuses to write owner:
// ownership moves through transfer, in one transaction, or an organization ends
// up with two owners or none.
func (r *repo) SetRole(ctx context.Context, orgID, userID, role string) error {
	if !IsGrantableRole(role) {
		return fmt.Errorf("role %q is not grantable", role)
	}
	ok, err := database.ConditionalUpdate(ctx, r.db, r.membershipsName, orgPK(orgID), aws.String(memberSK(userID)),
		map[string]any{"role": role},
		"attribute_exists(pk) AND #role <> :owner",
		map[string]string{"#role": "role"},
		map[string]types.AttributeValue{":owner": &types.AttributeValueMemberS{Value: RoleOwner}})
	if err != nil {
		return fmt.Errorf("changing role: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// RemoveMembership refuses to remove the owner, for the same reason SetRole
// refuses to write one: an organization with no owner has no one who can
// restore it.
func (r *repo) RemoveMembership(ctx context.Context, orgID, userID string) error {
	_, err := r.memberships.DeleteItemRaw(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(r.membershipsName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: orgPK(orgID)},
			"sk": &types.AttributeValueMemberS{Value: memberSK(userID)},
		},
		ConditionExpression:       aws.String("attribute_exists(pk) AND #role <> :owner"),
		ExpressionAttributeNames:  map[string]string{"#role": "role"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":owner": &types.AttributeValueMemberS{Value: RoleOwner}},
	})
	if database.IsConditionFailed(err) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("removing membership: %w", err)
	}
	return nil
}

// TransferOwnership demotes the old owner, promotes the new one and repoints
// the organization's owner_user_id — three writes, one transaction.
//
// Every item carries the condition that describes the state it expects to
// replace, so two transfers racing each other cannot both commit: the second
// finds a role it did not expect and the whole transaction is rejected. Doing
// this as three sequential writes would have two failure windows, and both
// leave an organization with either two owners or none.
func (r *repo) TransferOwnership(ctx context.Context, orgID, fromUserID, toUserID string, now time.Time) error {
	ownerVal := map[string]types.AttributeValue{":owner": &types.AttributeValueMemberS{Value: RoleOwner}}
	demote := map[string]types.AttributeValue{
		":owner": &types.AttributeValueMemberS{Value: RoleOwner},
		":admin": &types.AttributeValueMemberS{Value: RoleAdmin},
	}
	roleName := map[string]string{"#role": "role"}

	err := r.memberships.TransactWrite(ctx, []types.TransactWriteItem{
		// The outgoing owner becomes an admin, not a stranger: taking away the
		// workspace they built as a side effect of handing it over is a
		// surprise nobody asked for.
		r.memberships.BuildRawUpdateTxItem(orgPK(orgID), aws.String(memberSK(fromUserID)),
			"SET #role = :admin", "attribute_exists(pk) AND #role = :owner", roleName, demote),
		r.memberships.BuildRawUpdateTxItem(orgPK(orgID), aws.String(memberSK(toUserID)),
			"SET #role = :owner", "attribute_exists(pk) AND #role <> :owner", roleName, ownerVal),
		r.orgs.BuildRawUpdateTxItem(orgPK(orgID), aws.String(metaSK),
			"SET owner_user_id = :to, updated_at = :now", "attribute_exists(pk)", nil,
			map[string]types.AttributeValue{
				":to":  &types.AttributeValueMemberS{Value: toUserID},
				":now": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
			}),
	})
	if database.IsConditionFailed(err) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("transferring ownership: %w", err)
	}
	return nil
}

func (r *repo) PutInvitation(ctx context.Context, inv *Invitation) error {
	item, err := attributevalue.MarshalMap(inv)
	if err != nil {
		return fmt.Errorf("marshaling invitation: %w", err)
	}
	item["pk"] = &types.AttributeValueMemberS{Value: orgPK(inv.OrganizationID)}
	item["sk"] = &types.AttributeValueMemberS{Value: inviteSK(inv.Email)}
	// The index is keyed on the hash, so acceptance is one lookup from the
	// token the invitee holds — without the token ever being stored.
	item["lookup_pk"] = &types.AttributeValueMemberS{Value: inv.TokenHash}
	// DynamoDB TTL reaps the row: an invitation nobody accepted stops being a
	// standing offer without anything having to run.
	item["expires_at"] = &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", inv.ExpiresAt.Unix())}
	if err := r.invitations.PutItem(ctx, item); err != nil {
		return fmt.Errorf("writing invitation: %w", err)
	}
	return nil
}

func (r *repo) GetInvitationByToken(ctx context.Context, tokenHash string) (*Invitation, error) {
	res, err := r.invitations.QueryGSI(ctx, lookupIndex, "lookup_pk", tokenHash, 1, nil)
	if err != nil {
		return nil, fmt.Errorf("reading invitation: %w", err)
	}
	if len(res.Items) == 0 {
		return nil, ErrNotFound
	}
	return unmarshalInvitation(res.Items[0])
}

func (r *repo) ListInvitations(ctx context.Context, orgID string) ([]*Invitation, error) {
	res, err := r.invitations.Query(ctx, database.QueryOpts{
		PK: orgPK(orgID), SKPrefix: inviteSKPrefix, Limit: 200,
	})
	if err != nil {
		return nil, fmt.Errorf("listing invitations: %w", err)
	}
	out := make([]*Invitation, 0, len(res.Items))
	for _, item := range res.Items {
		inv, err := unmarshalInvitation(item)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, nil
}

func unmarshalInvitation(item map[string]types.AttributeValue) (*Invitation, error) {
	var inv Invitation
	if err := attributevalue.UnmarshalMap(item, &inv); err != nil {
		return nil, fmt.Errorf("unmarshaling invitation: %w", err)
	}
	if pk, ok := item["pk"].(*types.AttributeValueMemberS); ok {
		inv.OrganizationID = orgIDFromPK(pk.Value)
	}
	if sk, ok := item["sk"].(*types.AttributeValueMemberS); ok {
		inv.Email = emailFromSK(sk.Value)
	}
	if exp, ok := item["expires_at"].(*types.AttributeValueMemberN); ok {
		var secs int64
		if _, err := fmt.Sscanf(exp.Value, "%d", &secs); err == nil {
			inv.ExpiresAt = time.Unix(secs, 0).UTC()
		}
	}
	return &inv, nil
}

func (r *repo) DeleteInvitation(ctx context.Context, orgID, email string) error {
	if _, err := r.invitations.DeleteItem(ctx, orgPK(orgID), inviteSK(email)); err != nil {
		return fmt.Errorf("deleting invitation: %w", err)
	}
	return nil
}
