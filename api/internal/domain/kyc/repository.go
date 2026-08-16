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
	"gopkg.aoctech.app/account/api/internal/database"
	"gopkg.aoctech.app/account/api/internal/domain/risk"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

// usersTable matches the table used by the user repository — the CPF
// uniqueness item lives next to the user items (single-table pattern).
const usersTable = "account_users"

const conditionalCheckFailed = "ConditionalCheckFailed"

// BasicRecord is a validated Basic submission as persisted on the user item.
// Address is collected here (not Enhanced) — the planned BaaS integration
// needs it at the level every verified user reaches.
type BasicRecord struct {
	CPF         string
	LegalName   string
	BirthDate   string
	PhoneNumber string
	Address     Address
	SubmittedAt string
}

// Repository persists KYC state on the user item plus a CPF_{cpf}
// uniqueness item, transactionally.
type Repository interface {
	GetUser(ctx context.Context, userID string) (*user.User, error)

	// SaveBasicSubmission writes Basic identity data, sets kyc_level=basic and
	// kyc_status=verified, and claims CPF_{cpf} transactionally (failing with
	// ErrCPFConflict if another account owns it, releasing CPF_{oldCPF} when
	// re-submitting with a different CPF). Only reachable while Basic is not
	// yet Basic-verified — see Service.SubmitBasic.
	SaveBasicSubmission(ctx context.Context, userID string, rec BasicRecord, oldCPF string) error

	// AddDocument appends an uploaded Enhanced document. Unlike the old
	// single-tier scheme there is no separate "awaiting files" doc status:
	// documents may accumulate any time Service.assertAcceptsDocuments allows
	// it, while the derived state stays basic_verified until SubmitEnhanced.
	AddDocument(ctx context.Context, userID string, doc Document) error
	// SavePendingDocument records the presigned upload intent (documentID →
	// type, content_type) so ConfirmDocument can reject a mismatched type
	// (SEC-018).
	SavePendingDocument(ctx context.Context, userID, documentID, docType, contentType string) error
	// GetPendingDocument returns the recorded intent for documentID, or nil
	// when none was presigned.
	GetPendingDocument(ctx context.Context, documentID string) (*PendingDocument, error)
	// DeletePendingDocument drops the intent once the upload is confirmed.
	DeletePendingDocument(ctx context.Context, documentID string) error

	// SaveEnhancedSubmission moves a basic/verified (or enhanced/rejected)
	// user to enhanced/pending. Documents were already uploaded and validated
	// by Service.SubmitEnhanced; no CPF transaction is needed since it was
	// already claimed at Basic time.
	SaveEnhancedSubmission(ctx context.Context, userID, submittedAt, expiresAt string) error
	MarkVerified(ctx context.Context, userID, verifiedAt string) error
	// MarkRejected records the rejection and clears kyc_documents: a rejected
	// submission's documents were judged insufficient, so a resubmission must
	// upload fresh ones.
	MarkRejected(ctx context.Context, userID, reason string) error

	// SaveRiskAssessment overwrites the latest risk snapshot — no history is
	// kept (spec §9).
	SaveRiskAssessment(ctx context.Context, userID string, a risk.Assessment) error

	// ListPendingKYC returns every user whose Enhanced submission is queued
	// for review, for cmd/kyc list. Operator-tool Scan, not a request path.
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

func (r *dynamoRepository) SaveBasicSubmission(ctx context.Context, userID string, rec BasicRecord, oldCPF string) error {
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
				UpdateExpression: aws.String(
					"SET cpf = :cpf, legal_name = :ln, birth_date = :bd, phone_number = :phone, address = :addr, " +
						"kyc_level = :lvl, kyc_status = :st, kyc_submitted_at = :sub, kyc_basic_verified_at = :verified, updated_at = :now " +
						"REMOVE kyc_rejection_reason, phone_verified_at",
				),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":cpf":      &types.AttributeValueMemberS{Value: rec.CPF},
					":ln":       &types.AttributeValueMemberS{Value: rec.LegalName},
					":bd":       &types.AttributeValueMemberS{Value: rec.BirthDate},
					":phone":    &types.AttributeValueMemberS{Value: rec.PhoneNumber},
					":addr":     address,
					":lvl":      &types.AttributeValueMemberS{Value: LevelBasic},
					":st":       &types.AttributeValueMemberS{Value: StatusVerified},
					":sub":      &types.AttributeValueMemberS{Value: rec.SubmittedAt},
					":verified": &types.AttributeValueMemberS{Value: rec.SubmittedAt},
					":now":      &types.AttributeValueMemberS{Value: now},
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

func (r *dynamoRepository) AddDocument(ctx context.Context, userID string, doc Document) error {
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
			"SET kyc_documents = list_append(if_not_exists(kyc_documents, :empty), :doc), updated_at = :now",
		),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":empty": &types.AttributeValueMemberL{Value: []types.AttributeValue{}},
			":doc":   docAV,
			":now":   &types.AttributeValueMemberS{Value: now},
		},
	}
	_, err = r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Update: &update}}})
	return err
}

// pendingPKPrefix keys the standalone item holding a presigned upload intent.
const pendingPKPrefix = "KYCPEND_"

func buildPendingPK(documentID string) string { return pendingPKPrefix + documentID }

func (r *dynamoRepository) SavePendingDocument(ctx context.Context, userID, documentID, docType, contentType string) error {
	item, err := attributevalue.MarshalMap(map[string]string{
		"pk":           buildPendingPK(documentID),
		"user_id":      userID,
		"doc_type":     docType,
		"content_type": contentType,
	})
	if err != nil {
		return fmt.Errorf("marshaling pending document: %w", err)
	}
	if _, err := r.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(r.table),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	}); err != nil {
		return fmt.Errorf("saving pending document %s: %w", documentID, err)
	}
	return nil
}

func (r *dynamoRepository) GetPendingDocument(ctx context.Context, documentID string) (*PendingDocument, error) {
	out, err := r.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: buildPendingPK(documentID)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("getting pending document %s: %w", documentID, err)
	}
	if len(out.Item) == 0 {
		return nil, nil
	}
	var p PendingDocument
	if err := attributevalue.UnmarshalMap(out.Item, &p); err != nil {
		return nil, fmt.Errorf("unmarshaling pending document %s: %w", documentID, err)
	}
	return &p, nil
}

func (r *dynamoRepository) DeletePendingDocument(ctx context.Context, documentID string) error {
	if _, err := r.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(r.table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: buildPendingPK(documentID)},
		},
	}); err != nil {
		return fmt.Errorf("deleting pending document %s: %w", documentID, err)
	}
	return nil
}

func (r *dynamoRepository) SaveEnhancedSubmission(ctx context.Context, userID, submittedAt, expiresAt string) error {
	return r.userRepo.Update(ctx, userID, map[string]any{
		"kyc_level":            LevelEnhanced,
		"kyc_status":           StatusPending,
		"kyc_submitted_at":     submittedAt,
		"kyc_expires_at":       expiresAt,
		"kyc_rejection_reason": "",
	})
}

func (r *dynamoRepository) MarkVerified(ctx context.Context, userID, verifiedAt string) error {
	return r.userRepo.Update(ctx, userID, map[string]any{
		"kyc_status":           StatusVerified,
		"kyc_verified_at":      verifiedAt,
		"kyc_rejection_reason": "",
	})
}

func (r *dynamoRepository) MarkRejected(ctx context.Context, userID, reason string) error {
	if err := r.userRepo.Update(ctx, userID, map[string]any{
		"kyc_status":           StatusRejected,
		"kyc_rejection_reason": reason,
	}); err != nil {
		return err
	}
	// Documents were judged insufficient — clear them so re-submission requires
	// a fresh upload instead of silently reusing the rejected ones.
	update := types.Update{
		TableName: aws.String(r.table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: user.BuildPK(userID)},
		},
		UpdateExpression: aws.String("REMOVE kyc_documents"),
	}
	_, err := r.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{{Update: &update}}})
	return err
}

func (r *dynamoRepository) SaveRiskAssessment(ctx context.Context, userID string, a risk.Assessment) error {
	signals := make([]string, len(a.Signals))
	for i, s := range a.Signals {
		signals[i] = s.Name + ":" + s.Detail
	}
	return r.userRepo.Update(ctx, userID, map[string]any{
		"kyc_risk_score":        a.Score,
		"kyc_risk_signals":      signals,
		"kyc_risk_evaluated_at": a.EvaluatedAt,
	})
}

// ListPendingKYC scans for users whose Enhanced submission is queued for
// review.
// ponytail: offline operator tool (cmd/kyc list), not a request path — a GSI
// on kyc_status is the scale upgrade if this table grows large.
func (r *dynamoRepository) ListPendingKYC(ctx context.Context) ([]*user.User, error) {
	table := r.table
	var users []*user.User
	var startKey map[string]types.AttributeValue
	for {
		out, err := r.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:        aws.String(table),
			FilterExpression: aws.String("kyc_level = :lvl AND kyc_status = :st"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":lvl": &types.AttributeValueMemberS{Value: LevelEnhanced},
				":st":  &types.AttributeValueMemberS{Value: StatusPending},
			},
			ExclusiveStartKey: startKey,
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
