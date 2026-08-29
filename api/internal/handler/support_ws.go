package handler

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"uuid"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
	goproto "google.golang.org/protobuf/proto"
	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/domain/support"
	"gopkg.aoctech.app/account/api/internal/domain/user"
	supportproto "gopkg.aoctech.app/account/api/internal/proto"
	commonws "gopkg.aoctech.app/api-commons/ws"
)

const (
	supportWSAuthTimeout     = 5 * time.Second
	supportWSPongWait        = 45 * time.Second
	supportWSWriteWait       = 5 * time.Second
	supportWSMaxMessageBytes = 32 * 1024
)

func supportChannel(ticketID string) string      { return "support#" + ticketID }
func supportAgentChannel(ticketID string) string { return supportChannel(ticketID) + "#agents" }

type supportWSNotifier struct{ registry commonws.Registry }

func NewSupportWSNotifier(registry commonws.Registry) support.Notifier {
	return supportWSNotifier{registry: registry}
}

func (n supportWSNotifier) publish(ctx context.Context, channel string, event support.Event) {
	payload, err := goproto.Marshal(&supportproto.ServerMessage{Type: event.Type, Event: &supportproto.TicketEvent{
		TicketId: event.TicketID, Status: event.Status, EscalationLevel: event.EscalationLevel,
		AuthorType: event.AuthorType, Body: event.Body, CreatedAt: event.CreatedAt,
	}})
	if err == nil {
		n.registry.Broadcast(ctx, channel, payload)
	}
}
func (n supportWSNotifier) Publish(ctx context.Context, event support.Event) {
	n.publish(ctx, supportChannel(event.TicketID), event)
}
func (n supportWSNotifier) PublishInternal(ctx context.Context, event support.Event) {
	n.publish(ctx, supportAgentChannel(event.TicketID), event)
}

type binaryWSConn struct {
	mu   sync.Mutex
	conn *fws.Conn
}

func (c *binaryWSConn) WriteMessage(_ int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(supportWSWriteWait)); err != nil {
		return err
	}
	return c.conn.WriteMessage(fws.BinaryMessage, data)
}

func wsOriginAllowed(ctx *fasthttp.RequestCtx, allowed []string) bool {
	origin := string(ctx.Request.Header.Peek("Origin"))
	return origin == "" || slices.Contains(allowed, origin)
}

func supportAgent(role string) bool {
	return role == user.SupportRoleAgent || role == user.SupportRoleManager || role == user.SupportRoleAdmin
}

func sendSupportWS(conn *binaryWSConn, message *supportproto.ServerMessage) error {
	payload, err := goproto.Marshal(message)
	if err != nil {
		return err
	}
	return conn.WriteMessage(fws.BinaryMessage, payload)
}

// RegisterSupportWS mounts the binary-protobuf ticket stream. Authentication
// is the first frame: either a bearer JWT or the ticket's anonymous token.
// The socket is notification-only; durable mutations remain on HTTP so e-mail,
// validation, audit, and idempotency cannot be bypassed.
func RegisterSupportWS(router fiber.Router, svc *support.Service, users *user.Service, jwtSvc *crypto.JWTService, registry commonws.Registry, allowedOrigins []string) {
	upgrader := fws.FastHTTPUpgrader{ReadBufferSize: 1024, WriteBufferSize: 1024, CheckOrigin: func(ctx *fasthttp.RequestCtx) bool { return wsOriginAllowed(ctx, allowedOrigins) }}
	router.Get("/support/tickets/:id/ws", func(c fiber.Ctx) error {
		ticketID := strings.Clone(c.Params("id"))
		return upgrader.Upgrade(c.RequestCtx(), func(raw *fws.Conn) {
			raw.SetReadLimit(supportWSMaxMessageBytes)
			conn := &binaryWSConn{conn: raw}
			_ = raw.SetReadDeadline(time.Now().Add(supportWSAuthTimeout))
			_, payload, err := raw.ReadMessage()
			if err != nil {
				_ = raw.Close()
				return
			}
			_ = raw.SetReadDeadline(time.Time{})
			var auth supportproto.ClientMessage
			if goproto.Unmarshal(payload, &auth) != nil || auth.Type != "auth" {
				_ = sendSupportWS(conn, &supportproto.ServerMessage{Type: "error", Code: "unauthorized"})
				_ = raw.Close()
				return
			}

			ctx := context.Background()
			agent := false
			if auth.Token != "" {
				claims, verifyErr := jwtSvc.Verify(auth.Token)
				if verifyErr != nil {
					// @aoctech/ws-client intentionally has one first-frame credential
					// slot. Anonymous portal clients place their opaque ticket token in
					// that slot; it is accepted only when it matches this exact ticket.
					if _, _, loadErr := svc.GetTicketForCaller(ctx, ticketID, "", auth.Token); loadErr != nil {
						_ = sendSupportWS(conn, &supportproto.ServerMessage{Type: "error", Code: "unauthorized"})
						_ = raw.Close()
						return
					}
					goto authenticated
				}
				userID, _ := claims["sub"].(string)
				if userID == "" {
					_ = sendSupportWS(conn, &supportproto.ServerMessage{Type: "error", Code: "unauthorized"})
					_ = raw.Close()
					return
				}
				if account, loadErr := users.GetByID(ctx, userID); loadErr == nil && supportAgent(account.SupportRole) {
					agent = true
					if _, _, loadErr = svc.GetTicketAdmin(ctx, ticketID); loadErr != nil {
						_ = sendSupportWS(conn, &supportproto.ServerMessage{Type: "error", Code: "forbidden"})
						_ = raw.Close()
						return
					}
				} else if _, _, loadErr := svc.GetTicketForCaller(ctx, ticketID, userID, ""); loadErr != nil {
					_ = sendSupportWS(conn, &supportproto.ServerMessage{Type: "error", Code: "forbidden"})
					_ = raw.Close()
					return
				}
			} else if _, _, loadErr := svc.GetTicketForCaller(ctx, ticketID, "", auth.AnonymousToken); loadErr != nil {
				_ = sendSupportWS(conn, &supportproto.ServerMessage{Type: "error", Code: "forbidden"})
				_ = raw.Close()
				return
			}

		authenticated:
			connID := uuid.New().String()
			registry.Register(supportChannel(ticketID), connID, conn)
			defer registry.Unregister(supportChannel(ticketID), connID)
			if agent {
				registry.Register(supportAgentChannel(ticketID), connID, conn)
				defer registry.Unregister(supportAgentChannel(ticketID), connID)
			}
			_ = sendSupportWS(conn, &supportproto.ServerMessage{Type: "connected"})

			_ = raw.SetReadDeadline(time.Now().Add(supportWSPongWait))
			for {
				_, payload, readErr := raw.ReadMessage()
				if readErr != nil {
					return
				}
				var msg supportproto.ClientMessage
				if goproto.Unmarshal(payload, &msg) != nil {
					_ = sendSupportWS(conn, &supportproto.ServerMessage{Type: "error", Code: "invalid_frame"})
					continue
				}
				if msg.Type == "ping" {
					_ = raw.SetReadDeadline(time.Now().Add(supportWSPongWait))
					_ = sendSupportWS(conn, &supportproto.ServerMessage{Type: "pong", ActionId: msg.ActionId})
				} else {
					_ = sendSupportWS(conn, &supportproto.ServerMessage{Type: "error", Code: "unsupported_command", ActionId: msg.ActionId})
				}
			}
		})
	})
}
