package transport_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/inflight"
	"github.com/hcd233/aris-mass/internal/config"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy/vo"
	"github.com/hcd233/aris-mass/internal/infrastructure/transport"
)

func TestUpstreamLogCorrelatesRetryAndSummarizesProviderError(t *testing.T) {
	t.Parallel()

	var sent atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Request-Id", "provider-request-1")
		if sent.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","code":429003,"param":"reasoning.effort","message":"private-provider-response"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"complete","choices":[]}`))
	}))
	t.Cleanup(upstream.Close)

	traceID := uuid.NewString()
	ctx := context.WithValue(t.Context(), constant.CtxKeyTraceID, traceID)
	ctx = context.WithValue(ctx, constant.CtxKeyEndpointAttempt, uint(2))
	proxy := transport.NewOpenAIProxy(inflight.NewTracker(), transport.NewEndpointGuard(nil))
	_, err := proxy.ForwardChatCompletion(ctx, vo.UpstreamEndpoint{BaseURL: upstream.URL, Model: "test-model", APIKey: "private-provider-key"},
		[]byte(`{"model":"test-model","messages":[{"content":"private-user-prompt"}],"reasoning":{"effort":"none"}}`))
	if err != nil {
		t.Fatal(err)
	}

	logs, err := os.ReadFile(filepath.Join(config.LogDirPath, constant.LogInfoFileName))
	if err != nil {
		t.Fatal(err)
	}
	var entries []string
	for entry := range strings.SplitSeq(string(logs), "\n") {
		if strings.Contains(entry, traceID) {
			entries = append(entries, entry)
		}
	}
	if len(entries) != 5 {
		t.Fatalf("expected two sends, two results and one retry, got %v", entries)
	}
	checks := [][]string{
		{`"event":"llm.upstream.request"`, `"upstreamAttempt":1`, `"reasoningEffort":"none"`},
		{`"event":"llm.upstream.result"`, `"upstreamAttempt":1`, `"upstreamStatusCode":429`, `"upstreamErrorCode":"429003"`, `"upstreamErrorParam":"reasoning.effort"`, `"upstreamRequestID":"provider-request-1"`},
		{`"event":"llm.upstream.retry"`, `"upstreamAttempt":1`},
		{`"event":"llm.upstream.request"`, `"upstreamAttempt":2`},
		{`"event":"llm.upstream.result"`, `"upstreamAttempt":2`, `"upstreamStatusCode":200`},
	}
	for i, entry := range entries {
		if strings.Contains(entry, "private-") || !strings.Contains(entry, `"endpointAttempt":2`) {
			t.Fatalf("unexpected log line %s", entry)
		}
		for _, field := range checks[i] {
			if !strings.Contains(entry, field) {
				t.Fatalf("log %d missing %s: %s", i, field, entry)
			}
		}
	}
}
