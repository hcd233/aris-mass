package log_middleware

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
	apiutil "github.com/hcd233/aris-mass/internal/api/util"
	"github.com/hcd233/aris-mass/internal/application/llmproxy/port"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/middleware"
)

type interruptedStream struct{}

func (interruptedStream) Read(_ context.Context, sink port.EventSink) error {
	if err := sink.WriteEvent("response.output_text.delta", []byte("private-stream-content")); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}

func (interruptedStream) Close() error { return nil }

func TestRequestLogCorrelatesStreamFailureAfterResponse(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Use(middleware.TraceMiddleware(), middleware.LogMiddleware(middleware.LogMiddlewareConfig{}))
	api := humafiber.New(app, huma.DefaultConfig("Stream test", "1"))
	huma.Get(api, "/api/test/stream", func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return apiutil.AdaptProxyResult(ctx, &port.StreamResult{
			Open: func() (port.Stream, error) { return interruptedStream{}, nil },
		}, nil, nil)
	})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/api/test/stream", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "event: response.output_text.delta\ndata: private-stream-content\n\n" {
		t.Fatalf("unexpected stream body %q, err = %v", body, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}

	traceID := resp.Header.Get(constant.HTTPHeaderTraceID)
	httpLog := traceLog(t, traceID, "http.request.finished")
	streamLog := traceLog(t, traceID, "llm.stream.finished")
	if !strings.Contains(httpLog, `"stream":true`) || !strings.Contains(streamLog, `"errorKind":"unexpected_eof"`) {
		t.Fatalf("unexpected logs %s\n%s", httpLog, streamLog)
	}
	if strings.Contains(httpLog+streamLog, "private-stream-content") {
		t.Fatal("stream content reached the logs")
	}
}
