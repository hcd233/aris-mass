package log_middleware

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/config"
	"github.com/hcd233/aris-mass/internal/middleware"
)

func TestRequestLogCorrelatesBusinessFailureWithoutPayload(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Use(middleware.TraceMiddleware(), middleware.LogMiddleware(middleware.LogMiddlewareConfig{
		SamplingRules: []middleware.LogSamplingRule{{Path: "/api/test", Interval: time.Hour}},
	}))
	app.Post("/api/test", func(c fiber.Ctx) error {
		if c.Query("id") == "1" {
			return c.SendStatus(fiber.StatusNoContent)
		}
		c.Type("json")
		return c.SendString(`{"error":{"code":10006,"message":"private-error-text"}}`)
	})

	send := func(id string) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/api/test?id="+id+"&code=private-oauth-code",
			strings.NewReader(`{"model":"gpt-test","stream":false,"messages":[{"content":"private-prompt"}],"api_key":"private-key"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		req.Header.Set(fiber.HeaderAuthorization, "Bearer private-bearer")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if id == "2" && resp.StatusCode != fiber.StatusOK {
			t.Fatalf("business error status = %d, want 200", resp.StatusCode)
		}
		return resp.Header.Get(constant.HTTPHeaderTraceID)
	}

	_ = send("1")
	traceID := send("2")
	entry := traceLog(t, traceID, "http.request.finished")
	var got struct {
		Status       int    `json:"status"`
		BizErrorCode int    `json:"bizErrorCode"`
		Outcome      string `json:"outcome"`
		Query        struct {
			ID string `json:"id"`
		} `json:"queryParams"`
		Request struct {
			Model        string `json:"model"`
			Stream       *bool  `json:"stream"`
			MessageCount int    `json:"messageCount"`
		} `json:"requestParams"`
	}
	if err := sonic.UnmarshalString(entry, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != 200 || got.BizErrorCode != 10006 || got.Outcome != "business_error" || got.Query.ID != "2" ||
		got.Request.Model != "gpt-test" || got.Request.Stream == nil || *got.Request.Stream || got.Request.MessageCount != 1 {
		t.Fatalf("unexpected request log %s", entry)
	}
	if strings.Contains(entry, "private-") {
		t.Fatalf("private data reached logs: %s", entry)
	}
}

func traceLog(t *testing.T, traceID, event string) string {
	t.Helper()
	if traceID == "" {
		t.Fatal("missing response trace ID")
	}
	logs, err := os.ReadFile(filepath.Join(config.LogDirPath, constant.LogInfoFileName))
	if err != nil {
		t.Fatal(err)
	}
	for entry := range strings.SplitSeq(string(logs), "\n") {
		if strings.Contains(entry, traceID) && strings.Contains(entry, `"event":"`+event+`"`) {
			return entry
		}
	}
	t.Fatalf("missing %s log for traceID %s", event, traceID)
	return ""
}
