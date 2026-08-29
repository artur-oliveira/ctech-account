package handler_test

import (
	"context"
	"net/http"
	"testing"

	"gopkg.aoctech.app/account/api/internal/domain/support"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

func TestSupportAdminNotesArePrivateAndClosedTicketIsImmutable(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	owner := app.registerUser(t, "ticket-owner@example.com", "pass1234", "Owner")
	agent := app.registerUser(t, "ticket-agent@example.com", "pass1234", "Agent")
	if err := app.userSvc.SetSupportRole(ctx, agent.ID(), user.SupportRoleAgent); err != nil {
		t.Fatal(err)
	}
	ticket, err := app.supportSvc.CreateTicket(ctx, support.CreateTicketInput{UserID: owner.ID(), SubjectCategory: support.CategoryAccount, Body: "Não consigo acessar minha conta desde a última atualização."})
	if err != nil {
		t.Fatal(err)
	}

	agentToken := app.issueToken(t, agent.ID())
	ownerToken := app.issueToken(t, owner.ID())
	note := app.doWithToken(http.MethodPost, "/v1.0/admin/support/tickets/"+ticket.ID()+"/notes", map[string]any{"body": "Escalar se a redefinição falhar novamente."}, agentToken)
	if note.StatusCode != http.StatusCreated {
		t.Fatalf("note: %d %s", note.StatusCode, bodyString(note))
	}

	adminThread := app.doWithToken(http.MethodGet, "/v1.0/admin/support/tickets/"+ticket.ID(), nil, agentToken)
	if adminThread.StatusCode != http.StatusOK {
		t.Fatalf("admin get: %d", adminThread.StatusCode)
	}
	var adminBody struct {
		InternalNotes []support.InternalNote `json:"internal_notes"`
	}
	readJSON(t, adminThread, &adminBody)
	if len(adminBody.InternalNotes) != 1 {
		t.Fatalf("got %d internal notes", len(adminBody.InternalNotes))
	}

	publicThread := app.doWithToken(http.MethodGet, "/v1.0/support/tickets/"+ticket.ID(), nil, ownerToken)
	var publicBody map[string]any
	readJSON(t, publicThread, &publicBody)
	if _, leaked := publicBody["internal_notes"]; leaked {
		t.Fatal("public response leaked internal_notes")
	}

	closed := app.doWithToken(http.MethodPut, "/v1.0/admin/support/tickets/"+ticket.ID()+"/status", map[string]any{"status": support.StatusClosed}, agentToken)
	if closed.StatusCode != http.StatusNoContent {
		t.Fatalf("close: %d %s", closed.StatusCode, bodyString(closed))
	}

	userReply := app.doWithToken(http.MethodPost, "/v1.0/support/tickets/"+ticket.ID()+"/reply", map[string]any{"body": "Quero responder mesmo depois do encerramento definitivo."}, ownerToken)
	if userReply.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("closed reply: %d", userReply.StatusCode)
	}
	reopen := app.doWithToken(http.MethodPut, "/v1.0/admin/support/tickets/"+ticket.ID()+"/status", map[string]any{"status": support.StatusOpen}, agentToken)
	if reopen.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("reopen: %d", reopen.StatusCode)
	}
}

func TestSupportMetricsRequiresAgentAndReturnsBuckets(t *testing.T) {
	app := newTestApp(t)
	regular := app.registerUser(t, "regular-metrics@example.com", "pass1234", "Regular")
	if response := app.doWithToken(http.MethodGet, "/v1.0/admin/support/metrics", nil, app.issueToken(t, regular.ID())); response.StatusCode != http.StatusForbidden {
		t.Fatalf("regular user metrics status = %d", response.StatusCode)
	}
	agent := app.registerUser(t, "agent-metrics@example.com", "pass1234", "Agent")
	if err := app.userSvc.SetSupportRole(context.Background(), agent.ID(), user.SupportRoleAgent); err != nil {
		t.Fatal(err)
	}
	if response := app.doWithToken(http.MethodGet, "/v1.0/admin/support/metrics", nil, app.issueToken(t, agent.ID())); response.StatusCode != http.StatusOK {
		t.Fatalf("agent metrics status = %d: %s", response.StatusCode, bodyString(response))
	}
}
