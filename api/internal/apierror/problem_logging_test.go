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
	fiberobs "gopkg.aoctech.app/api-commons/observability/fiber"
)

func TestProblemSendLogsCauseAndRequestIDWithoutExposingCause(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	app := fiber.New()
	app.Use(fiberobs.RequestID(fiberobs.RequestIDConfig{Generator: func() string { return "req-123" }}))
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
	for _, want := range []string{"http request failed", "request_id=req-123", `err="database offline"`, "status=500"} {
		if !strings.Contains(logLine, want) {
			t.Fatalf("log %q does not contain %q", logLine, want)
		}
	}
}
