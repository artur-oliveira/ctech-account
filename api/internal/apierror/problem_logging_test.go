package apierror

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/observability"
)

func TestProblemSendLogsCauseAndRequestIDWithoutExposingCause(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(observability.WithRequestID(c.Context(), "req-123"))
		return c.Next()
	})
	app.Get("/failure", func(c fiber.Ctx) error {
		return ServerError(c.Path()).WithCause(errors.New("database offline")).Send(c)
	})

	response, err := app.Test(httptest.NewRequest("GET", "/failure", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	if response.StatusCode != fiber.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.StatusCode)
	}
	if strings.Contains(string(body), "database offline") {
		t.Fatalf("internal cause leaked in response: %s", body)
	}
	logLine := logs.String()
	for _, want := range []string{"http request failed", "request_id=req-123", `error="database offline"`, "status=500"} {
		if !strings.Contains(logLine, want) {
			t.Fatalf("log %q does not contain %q", logLine, want)
		}
	}
}
