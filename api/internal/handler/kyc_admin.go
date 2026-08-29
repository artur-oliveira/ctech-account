package handler

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/kyc"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/middleware"
)

// KYCAdminHandler serves the human-review workspace. Route registration must
// always include RequireSupportRole(manager); the handler never accepts role
// or reviewer identity from request data.
type KYCAdminHandler struct {
	kyc   *kyc.Service
	audit *audit.Service
	users *user.Service
}

func NewKYCAdminHandler(kycSvc *kyc.Service, auditSvc *audit.Service, users *user.Service) *KYCAdminHandler {
	return &KYCAdminHandler{kyc: kycSvc, audit: auditSvc, users: users}
}

func (h *KYCAdminHandler) Register(r fiber.Router) {
	r.Get("/reviews", h.list)
	r.Get("/reviews/:user_id", h.get)
	r.Post("/reviews/:user_id/documents/access", h.documents)
	r.Post("/reviews/:user_id/decision", h.decision)
}

func (h *KYCAdminHandler) list(c fiber.Ctx) error {
	queue := c.Query("status", kyc.ReviewQueuePending)
	if queue != kyc.ReviewQueuePending && queue != kyc.ReviewQueueCompleted {
		return apierror.ValidationFailed("status: must be pending or completed.", c.Path()).Send(c)
	}
	users, err := h.kyc.ListKYCReviews(c.Context(), queue)
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	reviews := make([]fiber.Map, 0, len(users))
	for _, u := range users {
		reviews = append(reviews, reviewSummary(u))
	}
	return c.JSON(fiber.Map{"reviews": reviews})
}

func (h *KYCAdminHandler) get(c fiber.Ctx) error {
	u, err := h.kyc.GetUser(c.Context(), c.Params("user_id"))
	if err != nil {
		return h.problem(c, err)
	}
	if u.KYCLevel != kyc.LevelEnhanced {
		return apierror.NotFound("KYC review", c.Path()).Send(c)
	}

	events, _, err := h.audit.ListByUser(c.Context(), u.ID(), "", 100)
	if err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	history := make([]fiber.Map, 0)
	for _, e := range events {
		if e.EventType != audit.EventKYCDocumentsViewed && e.EventType != audit.EventKYCVerified && e.EventType != audit.EventKYCRejected {
			continue
		}
		history = append(history, fiber.Map{
			"event_type": e.EventType, "created_at": e.CreatedAt,
			"actor_id": e.Metadata["reviewer_id"], "actor_name": e.Metadata["reviewer_name"],
			"actor_role": e.Metadata["reviewer_role"], "reason_code": e.Metadata["reason_code"], "details": e.Metadata["reason"],
		})
	}

	return c.JSON(fiber.Map{
		"review":    reviewDetail(u),
		"audit_log": history,
	})
}

// documents emits ten-minute S3 URLs only after an explicit, authenticated
// access action. The audit row is attached to the subject user so the review
// detail can show every operator who accessed the files.
func (h *KYCAdminHandler) documents(c fiber.Ctx) error {
	u, err := h.kyc.GetUser(c.Context(), c.Params("user_id"))
	if err != nil {
		return h.problem(c, err)
	}
	if u.KYCLevel != kyc.LevelEnhanced || (u.KYCStatus != kyc.StatusPending && u.KYCStatus != kyc.StatusVerified) {
		return apierror.NotFound("KYC documents", c.Path()).Send(c)
	}
	actor, err := h.currentReviewer(c)
	if err != nil {
		return err
	}
	docs, err := h.kyc.DocumentURLs(c.Context(), u.ID())
	if err != nil {
		return h.problem(c, err)
	}
	if h.audit == nil {
		return apierror.ServerError(c.Path()).Send(c)
	}
	if err := h.audit.RecordStrict(c.Context(), audit.Entry{
		UserID: u.ID(), Type: audit.EventKYCDocumentsViewed, IP: clientIP(c),
		UserAgent: c.Get("User-Agent"), Metadata: reviewerMetadata(actor, ""),
	}); err != nil {
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	return c.JSON(fiber.Map{"documents": docs, "expires_in": int(kyc.PresignTTL.Seconds())})
}

type kycDecisionRequest struct {
	Decision   string `json:"decision" validate:"required,oneof=approve reject"`
	ReasonCode string `json:"reason_code" validate:"omitempty,oneof=document_unreadable document_incomplete document_mismatch selfie_mismatch data_mismatch suspected_fraud other"`
	Details    string `json:"details" validate:"omitempty,max=255"`
}

func (h *KYCAdminHandler) decision(c fiber.Ctx) error {
	var req kycDecisionRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	req.Details = strings.TrimSpace(req.Details)
	if req.Decision == kyc.DecisionReject && (req.ReasonCode == "" || (req.ReasonCode == kyc.RejectionOther && req.Details == "")) {
		return apierror.ValidationFailed("reason_code: required; details are required for other.", c.Path()).Send(c)
	}
	actor, err := h.currentReviewer(c)
	if err != nil {
		return err
	}
	targetID := c.Params("user_id")
	if err := h.kyc.ReviewBy(c.Context(), targetID, req.Decision, req.ReasonCode, req.Details, kyc.ReviewActor{ID: actor.ID(), Name: actor.DisplayOrFullName()}); err != nil {
		return h.problem(c, err)
	}
	eventType := audit.EventKYCVerified
	if req.Decision == kyc.DecisionReject {
		eventType = audit.EventKYCRejected
	}
	meta := reviewerMetadata(actor, req.Details)
	meta["decision"] = req.Decision
	meta["reason_code"] = req.ReasonCode
	recordAudit(c, h.audit, targetID, eventType, meta)
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *KYCAdminHandler) currentReviewer(c fiber.Ctx) (*user.User, error) {
	u, err := h.users.GetByID(c.Context(), middleware.GetUserID(c))
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return nil, apierror.Forbidden("Manager role required.", c.Path()).Send(c)
		}
		return nil, apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
	return u, nil
}

func reviewerMetadata(u *user.User, reason string) map[string]string {
	return map[string]string{
		"reviewer_id": u.ID(), "reviewer_name": u.DisplayOrFullName(),
		"reviewer_role": u.SupportRole, "reason": reason,
	}
}

func reviewSummary(u *user.User) fiber.Map {
	return fiber.Map{
		"user_id": u.ID(), "legal_name": u.LegalName, "submitted_at": u.KYCSubmittedAt,
		"status": u.KYCStatus, "risk_score": u.KYCRiskScore, "reviewed_at": u.KYCReviewedAt,
		"reviewed_by": u.KYCReviewedBy, "reviewed_by_name": u.KYCReviewedByName,
		"decision": u.KYCReviewDecision,
	}
}

func reviewDetail(u *user.User) fiber.Map {
	detail := reviewSummary(u)
	detail["cpf"] = u.CPF
	detail["birth_date"] = u.BirthDate
	detail["phone_number"] = u.PhoneNumber
	detail["address"] = u.Address
	detail["risk_signals"] = u.KYCRiskSignals
	detail["risk_evaluated_at"] = u.KYCRiskEvaluatedAt
	detail["documents"] = u.KYCDocuments
	detail["rejection_reason"] = u.KYCRejectionReason
	detail["rejection_code"] = u.KYCRejectionCode
	detail["expires_at"] = u.KYCExpiresAt
	return detail
}

func (h *KYCAdminHandler) problem(c fiber.Ctx, err error) error {
	return (&KYCHandler{}).sendKYCError(c, err)
}
