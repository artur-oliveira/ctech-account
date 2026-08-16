package handler

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/kyc"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/middleware"
	scopeCatalog "gopkg.aoctech.app/account/api/internal/scopes"
)

// KYCHandler serves the user-facing identity verification routes and the
// slim internal (service-to-service) read used by ctech-wallet. The human
// approve/reject decision is not an HTTP route — see cmd/kyc.
type KYCHandler struct {
	kycSvc *kyc.Service
	audit  *audit.Service
}

func NewKYCHandler(kycSvc *kyc.Service, auditSvc *audit.Service) *KYCHandler {
	return &KYCHandler{kycSvc: kycSvc, audit: auditSvc}
}

// Register mounts the user-facing routes on the account group. Everything
// that writes identity data sits behind step-up.
func (h *KYCHandler) Register(account fiber.Router, stepUp fiber.Handler) {
	account.Get("/kyc", middleware.RequireScope(scopeCatalog.AccountKYCRead), h.get)
	account.Post("/kyc/basic", middleware.RequireScope(scopeCatalog.AccountKYCWrite), stepUp, h.submitBasic)
	account.Post("/kyc/documents", middleware.RequireScope(scopeCatalog.AccountKYCWrite), stepUp, h.presignDocument)
	account.Post("/kyc/documents/confirm", middleware.RequireScope(scopeCatalog.AccountKYCWrite), stepUp, h.confirmDocument)
	account.Post("/kyc/enhanced", middleware.RequireScope(scopeCatalog.AccountKYCWrite), stepUp, h.submitEnhanced)
}

// RegisterInternalGet mounts the one service-to-service route ctech-wallet
// still needs: the raw (unmasked) identity record for withdrawal-key
// validation. internalAuth = RequireAuth + RequireInternalScope(scopes.InternalWalletConfirmDeposit).
func (h *KYCHandler) RegisterInternalGet(v1 fiber.Router, internalAuth ...fiber.Handler) {
	handlers := make([]any, len(internalAuth))
	for i, m := range internalAuth {
		handlers[i] = m
	}
	grp := v1.Group("/internal/kyc", handlers...)
	grp.Get("/:user_id", h.internalGet)
}

type addressRequest struct {
	ZipCode    string `json:"zip_code" validate:"required,len=8,numeric"`
	Street     string `json:"street" validate:"required,max=200"`
	Number     string `json:"number" validate:"required,max=20"`
	Complement string `json:"complement" validate:"omitempty,max=100"`
	District   string `json:"district" validate:"required,max=100"`
	City       string `json:"city" validate:"required,max=100"`
	State      string `json:"state" validate:"required,len=2,alpha"`
}

func (r addressRequest) toDomain() kyc.Address {
	return kyc.Address{
		ZipCode:    r.ZipCode,
		Street:     r.Street,
		Number:     r.Number,
		Complement: r.Complement,
		District:   r.District,
		City:       r.City,
		State:      r.State,
	}
}

type submitBasicRequest struct {
	CPF         string         `json:"cpf" validate:"required,len=11,numeric"`
	LegalName   string         `json:"legal_name" validate:"required,min=3,max=200"`
	BirthDate   string         `json:"birth_date" validate:"required,datetime=2006-01-02"`
	PhoneNumber string         `json:"phone_number" validate:"required,e164"`
	Address     addressRequest `json:"address" validate:"required"`
}

// submitBasic validates and stores CPF/name/birthdate/phone/address, then
// grants Basic KYC without a separate phone-verification step.
func (h *KYCHandler) submitBasic(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req submitBasicRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	sub := kyc.BasicSubmission{
		CPF: req.CPF, LegalName: req.LegalName, BirthDate: req.BirthDate,
		PhoneNumber: req.PhoneNumber, Address: req.Address.toDomain(),
	}
	if err := h.kycSvc.SubmitBasic(c.Context(), userID, clientIP(c), sub); err != nil {
		return h.sendKYCError(c, err)
	}

	recordAudit(c, h.audit, userID, audit.EventKYCSubmitted, map[string]string{"level": kyc.LevelBasic})
	return h.sendStatus(c, userID)
}

func (h *KYCHandler) get(c fiber.Ctx) error {
	return h.sendStatus(c, middleware.GetUserID(c))
}

type presignDocumentRequest struct {
	Type        string `json:"type" validate:"required,oneof=id_front id_back selfie_with_document"`
	ContentType string `json:"content_type" validate:"required"`
}

// presignDocument hands the browser a short-lived S3 upload URL. The API
// never receives the file itself.
func (h *KYCHandler) presignDocument(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req presignDocumentRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	documentID, uploadURL, err := h.kycSvc.PresignDocument(c.Context(), userID, req.Type, req.ContentType)
	if err != nil {
		return h.sendKYCError(c, err)
	}

	return c.JSON(fiber.Map{
		"document_id":  documentID,
		"upload_url":   uploadURL,
		"expires_in":   int(kyc.PresignTTL.Seconds()),
		"max_bytes":    int64(kyc.MaxDocumentBytes),
		"content_type": req.ContentType,
	})
}

type confirmDocumentRequest struct {
	DocumentID string `json:"document_id" validate:"required,uuid4"`
	Type       string `json:"type" validate:"required,oneof=id_front id_back selfie_with_document"`
}

func (h *KYCHandler) confirmDocument(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	var req confirmDocumentRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}

	if err := h.kycSvc.ConfirmDocument(c.Context(), userID, req.DocumentID, req.Type); err != nil {
		return h.sendKYCError(c, err)
	}

	recordAudit(c, h.audit, userID, audit.EventKYCDocumentUploaded, map[string]string{"type": req.Type})
	return h.sendStatus(c, userID)
}

// submitEnhanced finalizes an Enhanced submission that already has every
// required document uploaded and queues it for human review.
func (h *KYCHandler) submitEnhanced(c fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	if err := h.kycSvc.SubmitEnhanced(c.Context(), userID, clientIP(c)); err != nil {
		return h.sendKYCError(c, err)
	}

	recordAudit(c, h.audit, userID, audit.EventKYCSubmitted, map[string]string{"level": kyc.LevelEnhanced})
	return h.sendStatus(c, userID)
}

// internalGet returns the full (unmasked) identity record — service-to-service
// only; the wallet needs the raw CPF for withdrawal key validation, and (for
// Asaas BaaS subaccount onboarding) the email already collected at
// registration alongside phone_number/address.
func (h *KYCHandler) internalGet(c fiber.Ctx) error {
	u, err := h.kycSvc.GetUser(c.Context(), c.Params("user_id"))
	if err != nil {
		return h.sendKYCError(c, err)
	}
	return c.JSON(fiber.Map{
		"level":        u.KYCLevel,
		"status":       u.KYCStatus,
		"cpf":          u.CPF,
		"legal_name":   u.LegalName,
		"birth_date":   u.BirthDate,
		"email":        u.Email,
		"phone_number": u.PhoneNumber,
		"address":      u.Address,
	})
}

// sendStatus is the single response shape of every user-facing KYC write, so
// the client never has to re-fetch to learn the new state.
func (h *KYCHandler) sendStatus(c fiber.Ctx, userID string) error {
	st, err := h.kycSvc.Get(c.Context(), userID)
	if err != nil {
		return h.sendKYCError(c, err)
	}
	return c.JSON(st)
}

// sendKYCError maps every domain error of this package to its RFC 7807 problem.
func (h *KYCHandler) sendKYCError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, kyc.ErrInvalidCPF):
		return apierror.ValidationFailed("cpf: invalid CPF.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrInvalidBirthDate):
		return apierror.ValidationFailed("birth_date: invalid date.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrInvalidPhone):
		return apierror.ValidationFailed("phone_number: invalid phone number.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrInvalidAddress):
		return apierror.ValidationFailed("address: invalid address.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrInvalidMethod):
		return apierror.ValidationFailed("method: document verification is not available.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrInvalidDocumentType):
		return apierror.ValidationFailed("type: unsupported document type.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrInvalidContentType):
		return apierror.ValidationFailed("content_type: unsupported document content type.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrInvalidDecision):
		return apierror.ValidationFailed("decision: must be approve or reject.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrUnderage):
		return apierror.AgeRequirementNotMet(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrAlreadyVerified):
		return apierror.KYCAlreadyVerified(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrBasicLocked):
		return apierror.KYCAlreadyVerified(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrBasicRequired):
		return apierror.KYCBasicRequired(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrCPFConflict):
		return apierror.CPFAlreadyRegistered(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrSubmissionLocked):
		return apierror.KYCSubmissionLocked(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrNotSubmitted), errors.Is(err, kyc.ErrNoDocuments):
		return apierror.KYCNotSubmitted(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrDocumentNotUploaded):
		return apierror.KYCDocumentNotUploaded(c.Path()).Send(c)
	case errors.Is(err, kyc.ErrDocumentTooLarge):
		return apierror.KYCDocumentTooLarge(kyc.MaxDocumentBytes, c.Path()).Send(c)
	case errors.Is(err, kyc.ErrTooManyDocuments):
		return apierror.ValidationFailed("documents: too many documents for this submission.", c.Path()).Send(c)
	case errors.Is(err, kyc.ErrDocumentTypeMismatch):
		return apierror.KYCDocumentNotUploaded(c.Path()).Send(c)
	case errors.Is(err, user.ErrNotFound):
		return apierror.NotFound("User", c.Path()).Send(c)
	}
	return apierror.ServerError(c.Path()).Send(c)
}
