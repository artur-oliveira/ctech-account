package handler

import (
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/support"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/email"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/api-commons/observability"
)

type SupportAdminHandler struct {
	svc    *support.Service
	users  *user.Service
	email  *email.Client
	appURL string
}

func NewSupportAdminHandler(svc *support.Service, users *user.Service, e *email.Client, appURL string) *SupportAdminHandler {
	return &SupportAdminHandler{svc: svc, users: users, email: e, appURL: appURL}
}
func (h *SupportAdminHandler) Register(r fiber.Router) {
	r.Get("/support/tickets", h.list)
	r.Get("/support/tickets/:id", h.get)
	r.Post("/support/tickets/:id/reply", h.reply)
	r.Put("/support/tickets/:id/status", h.status)
}
func (h *SupportAdminHandler) list(c fiber.Ctx) error {
	status := c.Query("status", support.StatusOpen)
	t, n, e := h.svc.ListByStatus(c.Context(), status, c.Query("cursor"), 50)
	if e != nil {
		return apierror.ServerError(c.Path()).WithCause(e).Send(c)
	}
	return c.JSON(fiber.Map{"tickets": t, "next_cursor": n})
}
func (h *SupportAdminHandler) get(c fiber.Ctx) error {
	t, m, e := h.svc.GetTicketAdmin(c.Context(), c.Params("id"))
	if e != nil {
		return supportProblem(c, e)
	}
	return c.JSON(fiber.Map{"ticket": t, "messages": m})
}

type adminTicketReplyRequest struct {
	Body string `json:"body" validate:"required,max=4200"`
}

// reply appends the agent's message, then e-mails it to the ticket's owner
// (account e-mail if authenticated, submitted address otherwise), threaded
// via In-Reply-To/References onto the prior message — this is the entire
// point of the admin UI: the agent never exposes a personal address.
func (h *SupportAdminHandler) reply(c fiber.Ctx) error {
	var r adminTicketReplyRequest
	if e := parseBody(c, &r); e != nil {
		return e
	}
	m, t, e := h.svc.ReplyAsAgent(c.Context(), c.Params("id"), middleware.GetUserID(c), r.Body)
	if e != nil {
		return supportProblem(c, e)
	}
	if h.email != nil {
		to, resolveErr := resolveTicketEmail(c.Context(), h.users, t)
		if resolveErr != nil {
			observability.Error(c.Context(), "support: failed to resolve ticket recipient", resolveErr,
				"ticket_id", t.ID(), "ticket_number", t.TicketNumber)
		} else if to != "" {
			references := t.RootSESMessageID
			if t.LastSESMessageID != "" && t.LastSESMessageID != t.RootSESMessageID {
				references = t.RootSESMessageID + " " + t.LastSESMessageID
			}
			mid, sendErr := h.email.SendTicketReplyEmail(c.Context(), to, t.TicketNumber, ticketSubjectLine(t), m.Body, t.LastSESMessageID, references, ticketLink(h.appURL, t))
			if sendErr != nil {
				observability.Error(c.Context(), "support: failed to send agent reply email", sendErr,
					"ticket_id", t.ID(), "ticket_number", t.TicketNumber)
			} else if err := h.svc.RecordEmailMessageID(c.Context(), t.ID(), mid, false); err != nil {
				observability.Error(c.Context(), "support: failed to persist reply email message id", err,
					"ticket_id", t.ID(), "ticket_number", t.TicketNumber)
			}
		}
	}
	return c.JSON(fiber.Map{"message": m, "ticket": t})
}

type adminTicketStatusRequest struct {
	Status string `json:"status" validate:"required,oneof=open answered closed"`
}

// status changes the ticket's status and, on a transition to closed, sends
// the NPS request e-mail once (guarded by nps_requested_at so re-closing an
// already-surveyed ticket doesn't spam a second request).
func (h *SupportAdminHandler) status(c fiber.Ctx) error {
	var r adminTicketStatusRequest
	if e := parseBody(c, &r); e != nil {
		return e
	}
	var beforeClose *support.Ticket
	if r.Status == support.StatusClosed {
		if t, _, e := h.svc.GetTicketAdmin(c.Context(), c.Params("id")); e == nil {
			beforeClose = t
		} else {
			observability.Error(c.Context(), "support: failed to load ticket before closing", e,
				"ticket_id", c.Params("id"))
		}
	}
	if e := h.svc.SetStatus(c.Context(), c.Params("id"), r.Status); e != nil {
		return supportProblem(c, e)
	}
	if beforeClose != nil && beforeClose.NPSRequestedAt == "" && h.email != nil {
		to, resolveErr := resolveTicketEmail(c.Context(), h.users, beforeClose)
		if resolveErr != nil {
			observability.Error(c.Context(), "support: failed to resolve NPS recipient", resolveErr,
				"ticket_id", beforeClose.ID(), "ticket_number", beforeClose.TicketNumber)
		} else if to != "" {
			npsLink := h.appURL + "/support/ticket?id=" + beforeClose.ID()
			if beforeClose.IsAnonymous() {
				npsLink += "&token=" + beforeClose.AnonymousToken
			}
			_, sendErr := h.email.SendTicketNPSEmail(c.Context(), to, beforeClose.TicketNumber, npsLink, beforeClose.LastSESMessageID, beforeClose.RootSESMessageID)
			if sendErr != nil {
				observability.Error(c.Context(), "support: failed to send NPS email", sendErr,
					"ticket_id", beforeClose.ID(), "ticket_number", beforeClose.TicketNumber)
			} else if err := h.svc.MarkNPSRequested(c.Context(), beforeClose.ID()); err != nil {
				observability.Error(c.Context(), "support: failed to persist NPS request marker", err,
					"ticket_id", beforeClose.ID(), "ticket_number", beforeClose.TicketNumber)
			}
		}
	}
	return c.SendStatus(fiber.StatusNoContent)
}
