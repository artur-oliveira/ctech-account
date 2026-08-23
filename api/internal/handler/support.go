package handler

import (
	"context"
	"errors"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/support"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/email"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/turnstile"
	"gopkg.aoctech.app/api-commons/observability"
)

type SupportHandler struct {
	svc      *support.Service
	users    *user.Service
	verifier turnstile.Verifier
	email    *email.Client
	appURL   string
}

const supportTicketTurnstileAction = "support_ticket"

func NewSupportHandler(s *support.Service, u *user.Service, v turnstile.Verifier, e *email.Client, appURL string) *SupportHandler {
	return &SupportHandler{svc: s, users: u, verifier: v, email: e, appURL: appURL}
}
func (h *SupportHandler) Register(r fiber.Router) {
	r.Post("/support/tickets", h.create)
	r.Get("/support/tickets/:id", h.get)
	r.Post("/support/tickets/:id/reply", h.reply)
	r.Post("/support/tickets/:id/nps", h.nps)
}
func (h *SupportHandler) RegisterAccount(r fiber.Router) { r.Get("/support/tickets", h.listMine) }

type supportCreateRequest struct {
	SubjectCategory string `json:"subject_category" validate:"required,oneof=account kyc wallet dfe billing poker other"`
	SubjectOther    string `json:"subject_other"`
	Priority        string `json:"priority" validate:"omitempty,oneof=low medium high urgent critical"`
	Body            string `json:"body" validate:"required,max=4200"`
	Email           string `json:"email" validate:"omitempty,email"`
	TurnstileToken  string `json:"turnstile_token" validate:"required"`
}

func (h *SupportHandler) create(c fiber.Ctx) error {
	var req supportCreateRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	if h.verifier != nil {
		if err := h.verifier.Verify(c.Context(), req.TurnstileToken, clientIP(c), supportTicketTurnstileAction); err != nil {
			return apierror.ValidationFailed("Turnstile verification failed.", c.Path()).WithCause(err).Send(c)
		}
	}
	id := middleware.GetUserID(c)
	if id == "" && req.Email == "" {
		return apierror.ValidationFailed("email is required for anonymous submissions.", c.Path()).Send(c)
	}
	t, err := h.svc.CreateTicket(c.Context(), support.CreateTicketInput{UserID: id, AnonymousEmail: req.Email, SubjectCategory: req.SubjectCategory, SubjectOther: req.SubjectOther, Priority: req.Priority, Body: req.Body})
	if err != nil {
		return supportProblem(c, err)
	}
	to := req.Email
	if id != "" && h.users != nil {
		if u, e := h.users.GetByID(c.Context(), id); e == nil {
			to = u.Email
		} else {
			observability.Error(c.Context(), "support: failed to resolve requester email", e,
				"ticket_id", t.ID(), "user_id", id)
		}
	}
	if h.email != nil && to != "" {
		mid, sendErr := h.email.SendTicketConfirmationEmail(c.Context(), to, t.TicketNumber, ticketSubjectLine(t), ticketLink(h.appURL, t))
		if sendErr != nil {
			observability.Error(c.Context(), "support: failed to send ticket confirmation email", sendErr,
				"ticket_id", t.ID(), "ticket_number", t.TicketNumber)
		} else if err := h.svc.RecordEmailMessageID(c.Context(), t.ID(), mid, true); err != nil {
			observability.Error(c.Context(), "support: failed to persist confirmation email message id", err,
				"ticket_id", t.ID(), "ticket_number", t.TicketNumber)
		}
	}
	out := fiber.Map{"ticket_id": t.ID(), "ticket_number": t.TicketNumber}
	if t.IsAnonymous() {
		out["anonymous_token"] = t.AnonymousToken
	}
	return c.Status(fiber.StatusCreated).JSON(out)
}
func (h *SupportHandler) get(c fiber.Ctx) error {
	t, m, e := h.svc.GetTicketForCaller(c.Context(), c.Params("id"), middleware.GetUserID(c), c.Query("token"))
	if e != nil {
		return supportProblem(c, e)
	}
	return c.JSON(fiber.Map{"ticket": t, "messages": m})
}

type supportReplyRequest struct {
	Body string `json:"body" validate:"required,max=4200"`
}

func (h *SupportHandler) reply(c fiber.Ctx) error {
	var r supportReplyRequest
	if e := parseBody(c, &r); e != nil {
		return e
	}
	if e := h.svc.ReplyAsUser(c.Context(), c.Params("id"), middleware.GetUserID(c), c.Query("token"), r.Body); e != nil {
		return supportProblem(c, e)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

type supportNPSRequest struct {
	Score   int    `json:"score" validate:"required,gte=1,lte=5"`
	Message string `json:"message"`
}

func (h *SupportHandler) nps(c fiber.Ctx) error {
	var r supportNPSRequest
	if e := parseBody(c, &r); e != nil {
		return e
	}
	if e := h.svc.SubmitNPS(c.Context(), c.Params("id"), middleware.GetUserID(c), c.Query("token"), r.Score, r.Message); e != nil {
		return supportProblem(c, e)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
func (h *SupportHandler) listMine(c fiber.Ctx) error {
	t, n, e := h.svc.ListMine(c.Context(), middleware.GetUserID(c), c.Query("cursor"), 20)
	if e != nil {
		return apierror.ServerError(c.Path()).WithCause(e).Send(c)
	}
	return c.JSON(fiber.Map{"tickets": t, "next_cursor": n})
}

// ticketLink builds the user-facing thread URL, including the anonymous
// token when the ticket has no owning user. Shared by the public and admin
// handlers so both send customers to the same place.
func ticketLink(appURL string, t *support.Ticket) string {
	l := appURL + "/support/ticket?id=" + t.ID()
	if t.IsAnonymous() {
		l += "&token=" + t.AnonymousToken
	}
	return l
}

// ticketSubjectLine is the human-readable subject used in e-mail
// subjects/bodies: the merged "category — subcategory" (or free-typed)
// label when present, falling back to the raw category slug for tickets
// created before that field was populated.
func ticketSubjectLine(t *support.Ticket) string {
	if t.SubjectOther != "" {
		return t.SubjectOther
	}
	return t.SubjectCategory
}

// resolveTicketEmail returns the address a ticket's notifications should go
// to: the bound account's e-mail for an authenticated ticket, or the
// anonymous submission address otherwise.
func resolveTicketEmail(ctx context.Context, users *user.Service, t *support.Ticket) (string, error) {
	if t.UserID != "" {
		if users == nil {
			return "", errors.New("user service is unavailable")
		}
		u, err := users.GetByID(ctx, t.UserID)
		if err != nil {
			return "", err
		}
		return u.Email, nil
	}
	return t.AnonymousEmail, nil
}

func supportProblem(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, support.ErrNotFound):
		return apierror.NotFound("Ticket", c.Path()).Send(c)
	case errors.Is(err, support.ErrForbidden):
		return apierror.Forbidden("Not authorized for this ticket.", c.Path()).Send(c)
	case errors.Is(err, support.ErrInvalidInput), errors.Is(err, support.ErrInvalidNPS):
		return apierror.ValidationFailed(err.Error(), c.Path()).Send(c)
	default:
		return apierror.ServerError(c.Path()).WithCause(err).Send(c)
	}
}
