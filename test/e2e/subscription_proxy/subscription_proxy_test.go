package subscriptionproxy_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hcd233/aris-mass/internal/bootstrap/modules"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/common/inflight"
	"github.com/hcd233/aris-mass/internal/config"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy/vo"
	"github.com/hcd233/aris-mass/internal/infrastructure/oauthprovider"
	"github.com/hcd233/aris-mass/internal/infrastructure/transport"
	"go.uber.org/fx"
)

func TestSubscriptionRequestsUseConfiguredProxy(t *testing.T) { //nolint:paralleltest // reloads global config and HTTP clients
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			_, _ = io.WriteString(w, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`) //nolint:errcheck // test response
		case "/usage":
			_, _ = io.WriteString(w, `{}`) //nolint:errcheck // test response
		case "/responses", "/v1/messages":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {}\n\n") //nolint:errcheck // test response
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	origin, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	forward := httputil.NewSingleHostReverseProxy(origin)
	forward.Transport = upstream.Client().Transport
	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() {
			t.Error("proxy request target must be an absolute URL")
		}
		proxyRequests.Add(1)
		forward.ServeHTTP(w, r)
	}))
	t.Cleanup(proxy.Close)
	startWithProxyConfig(t, proxy.URL)

	for _, provider := range enum.OAuthProviders {
		client := oauthprovider.NewClientWithURLs(provider, upstream.URL, upstream.URL+"/token", upstream.URL+"/usage")
		if _, err := client.ExchangeCode(t.Context(), "code", "verifier", "state"); err != nil {
			t.Fatalf("%s exchange: %v", provider, err)
		}
		if _, err := client.Refresh(t.Context(), "refresh"); err != nil {
			t.Fatalf("%s refresh: %v", provider, err)
		}
		if _, err := client.ProbeQuota(t.Context(), "access", "account"); err != nil {
			t.Fatalf("%s quota: %v", provider, err)
		}
	}

	tracker := inflight.NewTracker()
	openai := transport.NewOpenAIProxy(tracker, transport.NewEndpointGuard(nil))
	anthropic := transport.NewAnthropicProxy(tracker, transport.NewEndpointGuard(nil))
	body := []byte(`{"model":"model","stream":true}`)

	codex := vo.NewUpstreamEndpointFromCredential("model", "access", upstream.URL, enum.EndpointAuthOAuthOpenAI, "account", "oauth:1")
	stream, err := openai.OpenCreateResponseStream(t.Context(), codex, body)
	checkStream(t, stream, err)

	claude := vo.NewUpstreamEndpointFromCredential("model", "access", upstream.URL+"/v1", enum.EndpointAuthOAuthAnthropic, "account", "oauth:2")
	stream, err = anthropic.OpenCreateMessageStream(t.Context(), claude, body)
	checkStream(t, stream, err)

	if got := proxyRequests.Load(); got != 8 {
		t.Fatalf("proxy requests for 8 subscription calls = %d", got)
	}

	apiKey := vo.NewUpstreamEndpointFromCredential("model", "api-key", upstream.URL, enum.EndpointAuthAPIKey, "", "")
	stream, err = openai.OpenCreateResponseStream(t.Context(), apiKey, body)
	checkStream(t, stream, err)
	if got := proxyRequests.Load(); got != 8 {
		t.Fatalf("API Key request increased proxy request count to %d", got)
	}
}

func startWithProxyConfig(t *testing.T, raw string) {
	t.Helper()
	app := newProxyApp(t, raw)
	if err := app.Start(t.Context()); err != nil {
		t.Fatalf("start HTTP clients: %v", err)
	}
	t.Cleanup(func() {
		// t.Context() is canceled before cleanup callbacks.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Errorf("stop HTTP clients: %v", err)
		}
	})
}

func newProxyApp(t *testing.T, raw string) *fx.App {
	t.Helper()
	t.Cleanup(config.InitEnvironment)
	t.Setenv("OAUTH_SUBSCRIPTION_PROXY", raw)
	config.InitEnvironment()
	return fx.New(fx.NopLogger, fx.Invoke(modules.InitHTTPClient))
}

func checkStream(t *testing.T, stream io.ReadCloser, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("open model stream: %v", err)
	}
	defer stream.Close()
	body, err := io.ReadAll(stream)
	if err != nil || string(body) != "data: {}\n\n" {
		t.Fatalf("read model stream: body=%q, err=%v", body, err)
	}
}
