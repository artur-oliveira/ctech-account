package kyc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"gopkg.aoctech.app/account/internal/database"
	"gopkg.aoctech.app/account/internal/domain/user"
)

// usersTable matches the table used by the user repository — the CPF
// uniqueness item lives next to the user items (single-table pattern).
const usersTable = "account_users"

const conditionalCheckFailed = "ConditionalCheckFailed"

// Record is one full submission as persisted on the user item.
type Record struct {
	CPF         string
	LegalName   string
	BirthDate   string
	Method      string
	Address     Address
	DocStatus   string
	SubmittedAt string
	ExpiresAt   string
}

// Repository persists KYC state on the user item plus a CPF_{cpf}
// uniqueness item, transactionally.
type Repository interface {
	GetUser(ctx context.Context, userID string) (*user.User, error)
	// SaveSubmission writes the identity data and doc_status=pending_review on
	// the user (the caller has already verified every required document is
	// uploaded), claims CPF_{cpf} (failing with ErrCPFConflict if another
	// account owns it), and releases CPF_{oldCPF} when re-submitting with a
	// different CPF. It clears any previous rejection reason but leaves
	// kyc_documents untouched — those documents are the submission.
	SaveSubmission(ctx context.Context, userID string, rec Record, oldCPF string) error
	// AddDocument appends an uploaded document and sets the doc status
	// (awaiting_files while the user is still gathering required documents).
	AddDocument(ctx context.Context, userID string, doc Document, docStatus string) error
	MarkVerified(ctx context.Context, userID, verifiedAt string) error
	// MarkRejected records the rejection and clears kyc_documents: a rejected
	// submission's documents were judged insufficient, so a resubmission must
	// upload fresh ones.
	MarkRejected(ctx context.Context, userID, reason string) error
	// ListPendingKYC returns every user whose doc_status is pending_review, for
	// cmd/kyc list. This is an operator-tool Scan, not a request path.
	ListPendingKYC(ctx context.Context) ([]*user.User, error)
}

type dynamoRepository struct {
	db       *dynamodb.Client
	table    string
	userRepo user.Repository
}

// NewRepository returns a DynamoDB-backed Repository reusing the user
// repository for reads.
func NewRepository(db *dynamodb.Client, tablePrefix string, userRepo user.Repository) Repository {
	return &dynamoRepository{db: db, table: database.TableName(tablePrefix, usersTable), userRepo: userRepo}
}

func (r *dynamoRepository) GetUser(ctx context.Context, userID string) (*user.User, error) {
	return r.userRepo.GetByID(ctx, userID)
}

func (r *dynamoRepository) SaveSubmission(ctx context.Context, userID string, rec Record, oldCPF string) error {
	table := r.table
	now := time.Now().UTC().Format(time.RFC3339)

	cpfItem, err := attributevalue.MarshalMap(map[string]string{
		"pk":         BuildCPFPK(rec.CPF),
		"user_id":    userID,
		"created_at": now,
	})
	if err != nil {
		return fmt.Errorf("marshaling cpf item: %w", err)
	}

	address, err := attributevalue.Marshal(rec.Address)
	if err != nil {
		return fmt.Errorf("marshaling address: %w", err)
	}

	items := []types.TransactWriteItem{
		{
			Put: &types.Put{
				TableName: aws.String(table),
				Item:      cpfItem,
				// New claims require an unclaimed pk; a re-submission with the same
				// CPF finds the user's own item — not a conflict.
				ConditionExpression: aws.String("attribute_not_exists(pk) OR user_id = :uid"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":uid": &types.AttributeValueMemberS{Value: userID},
				},
			},
		},
		{
			Update: &types.Update{
				TableName: aws.String(table),
				Key: map[string]types.AttributeValue{
					"pk": &types.AttributeValueMemberS{Value: user.BuildPK(userID)},
				},
				// The documents backing this submission were already uploaded and
				// validated by Service.Submit — only identity data and the doc
				// status move here. A stale rejection reason must not survive it.
				UpdateExpression: aws.String(
					"SET cpf = :cpf, legal_name = :ln, birth_date = :bd, " +
						"kyc_method = :m, kyc_doc_status = :ds, kyc_submitted_at = :sub, " +
						"kyc_expires_at = :exp, address = :addr, updated_at = :now " +
						"REMOVE kyc_rejection_reason",
				),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":cpf":  &types.AttributeValueMemberS{Value: rec.CPF},
					":ln":   &types.AttributeValueMemberS{Value: rec.LegalName},
					":bd":   &types.AttributeValueMemberS{Value: rec.BirthDate},
					":m":    &types.AttributeValueMemberS{Value: rec.Method},
					":ds":   &types.AttributeValueMemberS{Value: rec.DocStatus},
					":sub":  &types.AttributeValueMemberS{Value: rec.SubmittedAt},
					":exp":  &types.AttributeValueMemberS{Value: rec.ExpiresAt},
					":addr": address,
					":now":  &types.AttributeValueMemberS{Value: now},
				},
			},
		},
	}

	if oldCPF != "" && oldCPF != rec.CPF {
		items = append(items, types.TransactWriteItem{
			Delete: &types.Delete{
				TableName: aws.String(table),
				Key: map[string]types.AttributeValue{
					"pk": &types.AttributeValueMemberS{Value: BuildCPFPK(oldCPF)},
				},
			},
		})
	}

	if _, err := r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items}); err != nil {
		var canceled *types.TransactionCanceledException
		if errors.As(err, &canceled) {
			for _, reason := range canceled.CancellationReasons {
				if reason.Code != nil && *reason.Code == conditionalCheckFailed {
					return ErrCPFConflict
				}
			}
		}
		return err
	}
	return nil
}

func (r *dynamoRepository) AddDocument(ctx context.Context, userID string, doc Document, docStatus string) error {
	table := r.table
	now := time.Now().UTC().Format(time.RFC3339)

	docAV, err := attributevalue.Marshal([]Document{doc})
	if err != nil {
		return fmt.Errorf("marshaling document: %w", err)
	}

	key := map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: user.BuildPK(userID)},
	}
	// list_append on a missing attribute errors, so seed it with an empty list.
	update := types.Update{
		TableName: aws.String(table),
		Key:       key,
		UpdateExpression: aws.String(
			"SET kyc_documents = list_append(if_not_exists(kyc_documents, :empty), :doc), " +
				"kyc_doc_status = :ds, updated_at = :now",
		),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":empty": &types.AttributeValueMemberL{Value: []types.AttributeValue{}},
			":doc":   docAV,
			":ds":    &types.AttributeValueMemberS{Value: docStatus},
			":now":   &types.AttributeValueMemberS{Value: now},
		},
	}
	_, err = r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Update: &update}}})
	return err
}

func (r *dynamoRepository) MarkVerified(ctx context.Context, userID, verifiedAt string) error {
	return r.userRepo.Update(ctx, userID, map[string]any{
		"kyc_level":            LevelVerified,
		"kyc_verified_at":      verifiedAt,
		"kyc_doc_status":       DocStatusNone,
		"kyc_rejection_reason": "",
	})
}

func (r *dynamoRepository) MarkRejected(ctx context.Context, userID, reason string) error {
	if err := r.userRepo.Update(ctx, userID, map[string]any{
		"kyc_doc_status":       DocStatusRejected,
		"kyc_rejection_reason": reason,
	}); err != nil {
		return err
	}
	// Documents were judged insufficient — clear them so re-submission requires
	// a fresh upload instead of silently reusing the rejected ones.
	table := r.table
	update := types.Update{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: user.BuildPK(userID)},
		},
		UpdateExpression: aws.String("REMOVE kyc_documents"),
	}
	_, err := r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Update: &update}}})
	return err
}

// ListPendingKYC scans for users whose submission is queued for review.
// ponytail: offline operator tool (cmd/kyc list), not a request path — a GSI
// on kyc_doc_status is the scale upgrade if this table grows large.
func (r *dynamoRepository) ListPendingKYC(ctx context.Context) ([]*user.User, error) {
	table := r.table
	var users []*user.User
	var startKey map[string]types.AttributeValue
	for {
		out, err := r.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 aws.String(table),
			FilterExpression:          aws.String("kyc_doc_status = :ds"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":ds": &types.AttributeValueMemberS{Value: DocStatusPendingReview}},
			ExclusiveStartKey:         startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("scanning for pending kyc: %w", err)
		}
		for _, item := range out.Items {
			var u user.User
			if err := attributevalue.UnmarshalMap(item, &u); err != nil {
				return nil, fmt.Errorf("unmarshaling user: %w", err)
			}
			users = append(users, &u)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return users, nil
}
