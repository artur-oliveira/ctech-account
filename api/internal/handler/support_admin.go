package handler

import (
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/domain/support"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/email"
	"gopkg.aoctech.app/account/api/internal/middleware"
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
		return apierror.ServerError(c.Path()).Send(c)
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
		if to := resolveTicketEmail(c.Context(), h.users, t); to != "" {
			references := t.RootSESMessageID
			if t.LastSESMessageID != "" && t.LastSESMessageID != t.RootSESMessageID {
				references = t.RootSESMessageID + " " + t.LastSESMessageID
			}
			mid, sendErr := h.email.SendTicketReplyEmail(c.Context(), to, t.TicketNumber, ticketSubjectLine(t), m.Body, t.LastSESMessageID, references, ticketLink(h.appURL, t))
			if sendErr == nil {
				_ = h.svc.RecordEmailMessageID(c.Context(), t.ID(), mid, false)
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
		}
	}
	if e := h.svc.SetStatus(c.Context(), c.Params("id"), r.Status); e != nil {
		return supportProblem(c, e)
	}
	if beforeClose != nil && beforeClose.NPSRequestedAt == "" && h.email != nil {
		if to := resolveTicketEmail(c.Context(), h.users, beforeClose); to != "" {
			npsLink := h.appURL + "/support/ticket?id=" + beforeClose.ID()
			if beforeClose.IsAnonymous() {
				npsLink += "&token=" + beforeClose.AnonymousToken
			}
			_, sendErr := h.email.SendTicketNPSEmail(c.Context(), to, beforeClose.TicketNumber, npsLink, beforeClose.LastSESMessageID, beforeClose.RootSESMessageID)
			if sendErr == nil {
				_ = h.svc.MarkNPSRequested(c.Context(), beforeClose.ID())
			}
		}
	}
	return c.SendStatus(fiber.StatusNoContent)
}
