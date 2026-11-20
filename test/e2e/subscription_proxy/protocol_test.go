package subscriptionproxy_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apernet/hysteria/core/v2/server"
	"github.com/apernet/hysteria/extras/v2/obfs"
	"github.com/bytedance/sonic"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/infrastructure/httpclient"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
)

const proxyTestTarget = "http://subscription-proxy.test.invalid"

func TestHysteria2SubscriptionProxyWithSalamander(t *testing.T) { //nolint:paralleltest // reloads global config and HTTP clients
	const password = "test-hysteria-password"
	const obfuscationPassword = "test-salamander-password"
	address := startHysteriaServer(t, hysteriaPassword(password), obfuscationPassword)
	startWithProxyConfig(t, hysteriaProxyConfig(t, address, password, obfuscationPassword))
	checkProxyResponse(t)
}

func TestHysteria2ShutdownCancelsAuthentication(t *testing.T) { //nolint:paralleltest // reloads global config and HTTP clients
	auth := waitingHysteriaAuthenticator{entered: make(chan struct{}), release: make(chan struct{})}
	address := startHysteriaServer(t, auth, "")
	t.Cleanup(func() { close(auth.release) })
	app := newProxyApp(t, hysteriaProxyConfig(t, address, "test-password", ""))
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, proxyTestTarget, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		resp, requestErr := httpclient.GetEndpointHTTPClient(enum.EndpointAuthOAuthOpenAI).Do(req)
		if resp != nil {
			_ = resp.Body.Close() //nolint:errcheck // closure is irrelevant to the assertion.
		}
		done <- requestErr
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	select {
	case <-auth.entered:
	case <-ctx.Done():
		t.Fatal("Hysteria2 proxy never reached authentication")
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("stop during Hysteria2 authentication: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("request succeeded during unfinished authentication")
		}
	case <-ctx.Done():
		t.Fatal("request did not finish after stopping the proxy")
	}
}

func startHysteriaServer(t *testing.T, auth server.Authenticator, obfuscationPassword string) net.Addr {
	t.Helper()
	certificate := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(certificate.Close)

	var listen net.ListenConfig
	listener, err := listen.ListenPacket(t.Context(), "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tunnel := listener
	if obfuscationPassword != "" {
		tunnel, err = obfs.WrapPacketConnSalamander(listener, []byte(obfuscationPassword))
		if err != nil {
			_ = listener.Close() //nolint:errcheck // test setup failed.
			t.Fatal(err)
		}
	}
	proxy, err := server.NewServer(&server.Config{
		Conn:          tunnel,
		TLSConfig:     server.TLSConfig{Certificates: certificate.TLS.Certificates},
		Authenticator: auth,
		Outbound:      hysteriaTestOutbound{ctx: t.Context(), address: newProxyTestOrigin(t)},
		DisableUDP:    true,
	})
	if err != nil {
		_ = tunnel.Close() //nolint:errcheck // test setup failed.
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = proxy.Serve() //nolint:errcheck // closing the server exits Serve.
	}()
	t.Cleanup(func() {
		if err := proxy.Close(); err != nil {
			t.Errorf("close Hysteria2 server: %v", err)
		}
		<-stopped
	})

	return listener.LocalAddr()
}

func hysteriaProxyConfig(t *testing.T, addr net.Addr, password, obfuscationPassword string) string {
	t.Helper()
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		t.Fatal(err)
	}
	method := ""
	if obfuscationPassword != "" {
		method = "salamander"
	}
	return fmt.Sprintf(`{"type":"Hysteria2","host":%q,"port":%q,"password":%q,"obfs":%q,"obfsParam":%q,"allowInsecure":1}`,
		host, port, password, method, obfuscationPassword,
	)
}

func TestVMessAndVLESSSubscriptionProxies(t *testing.T) { //nolint:paralleltest // reloads global config and HTTP clients
	for _, protocol := range []string{"vmess", "vless"} { //nolint:paralleltest // test updates shared HTTP client.
		t.Run(protocol, func(t *testing.T) {
			const nodeID = "10000000-0000-4000-8000-000000000001"
			listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}

			var serverConfig conf.Config
			if err := sonic.UnmarshalString(fmt.Sprintf(`{
				"log":{"loglevel":"none"},
				"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":%q,"settings":{"clients":[{"id":%q}],"decryption":"none"}}],
				"outbounds":[{"protocol":"freedom","settings":{"redirect":%q}}]
			}`, port, protocol, nodeID, newProxyTestOrigin(t)), &serverConfig); err != nil {
				t.Fatal(err)
			}
			built, err := serverConfig.Build()
			if err != nil {
				t.Fatal(err)
			}
			proxy, err := core.New(built)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := proxy.Close(); err != nil {
					t.Errorf("close Xray server: %v", err)
				}
			})
			if err := proxy.Start(); err != nil {
				t.Fatal(err)
			}

			startWithProxyConfig(t, fmt.Sprintf(
				`{"protocol":%q,"settings":{"vnext":[{"address":"127.0.0.1","port":%d,"users":[{"id":%q,"encryption":"none","security":"auto"}]}]}}`,
				protocol, port, nodeID,
			))
			checkProxyResponse(t)
		})
	}
}

func TestXrayShutdownClosesProxyHandshake(t *testing.T) { //nolint:paralleltest // reloads global config and HTTP clients
	var listen net.ListenConfig
	listener, err := listen.Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() }) //nolint:errcheck // test cleanup.
	port := listener.Addr().(*net.TCPAddr).Port
	app := newProxyApp(t, fmt.Sprintf(`{"protocol":"vless","settings":{"vnext":[{"address":"127.0.0.1","port":%d,"users":[{"id":"10000000-0000-4000-8000-000000000001","encryption":"none"}]}]},"streamSettings":{"security":"tls"}}`, port))
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyTestTarget, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan error, 1)
	go func() {
		resp, err := httpclient.GetEndpointHTTPClient(enum.EndpointAuthOAuthOpenAI).Do(req)
		if resp != nil {
			_ = resp.Body.Close() //nolint:errcheck // test cleanup.
		}
		requestDone <- err
	}()
	deadline, _ := ctx.Deadline()
	if err := listener.(*net.TCPListener).SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	peer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err := peer.SetReadDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	var firstByte [1]byte
	if _, err := io.ReadFull(peer, firstByte[:]); err != nil {
		t.Fatalf("read TLS handshake from Xray: %v", err)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("stop during Xray handshake: %v", err)
	}
	if _, err := io.Copy(io.Discard, peer); err != nil {
		t.Fatalf("proxy socket did not close: %v", err)
	}
	if err := <-requestDone; err == nil {
		t.Fatal("request succeeded during unfinished Xray handshake")
	}
}

func TestSubscriptionProxyFailureDoesNotDialDirect(t *testing.T) { //nolint:paralleltest // reloads global config and HTTP clients
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)
	startWithProxyConfig(t, proxy.URL)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("subscription traffic unexpectedly reached the origin directly")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := httpclient.GetEndpointHTTPClient(enum.EndpointAuthOAuthOpenAI).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected the proxy's status, got %d", resp.StatusCode)
	}
}

func TestInvalidSubscriptionProxyStopsStartup(t *testing.T) { //nolint:paralleltest // reloads global config and HTTP clients
	app := newProxyApp(t, `{"protocol":"freedom","settings":{"password":"test-secret"}}`)
	err := app.Start(t.Context())
	if err == nil || !strings.Contains(err.Error(), "OAUTH_SUBSCRIPTION_PROXY") {
		t.Fatalf("expected proxy configuration error, got %v", err)
	}
	if strings.Contains(err.Error(), "test-secret") {
		t.Fatal("proxy configuration error exposed a secret")
	}
}

func TestLiveSubscriptionProxy(t *testing.T) { //nolint:paralleltest // reloads global config and HTTP clients
	if os.Getenv("ARIS_TEST_LIVE_SUBSCRIPTION_PROXY") != "1" {
		t.Skip("set ARIS_TEST_LIVE_SUBSCRIPTION_PROXY=1 to probe the configured proxy")
	}
	raw := os.Getenv("OAUTH_SUBSCRIPTION_PROXY")
	if raw == "" {
		t.Fatal("OAUTH_SUBSCRIPTION_PROXY is required for the live probe")
	}
	startWithProxyConfig(t, raw)
	for _, target := range []struct { //nolint:paralleltest // probe upstream destinations in order.
		name string
		url  string
		auth enum.EndpointAuthType
	}{
		{"Codex OAuth", constant.OAuthOpenAITokenURL, enum.EndpointAuthOAuthOpenAI},
		{"Codex model", constant.OAuthOpenAIUpstreamBaseURL + "/responses", enum.EndpointAuthOAuthOpenAI},
		{"Claude OAuth", constant.OAuthClaudeTokenURL, enum.EndpointAuthOAuthAnthropic},
		{"Claude model", constant.OAuthClaudeUpstreamBaseURL + "/messages", enum.EndpointAuthOAuthAnthropic},
	} {
		t.Run(target.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.url, strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := httpclient.GetEndpointHTTPClient(target.auth).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			t.Log("HTTP " + strconv.Itoa(resp.StatusCode))
		})
	}
}

func checkProxyResponse(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyTestTarget, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := httpclient.GetEndpointHTTPClient(enum.EndpointAuthOAuthOpenAI).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "through the configured proxy" {
		t.Fatalf("upstream response = %q, err = %v", body, err)
	}
}

func newProxyTestOrigin(t *testing.T) string {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "through the configured proxy") //nolint:errcheck // test response.
	}))
	t.Cleanup(upstream.Close)
	return upstream.Listener.Addr().String()
}

type hysteriaPassword string

func (p hysteriaPassword) Authenticate(_ net.Addr, password string, _ uint64) (ok bool, id string) {
	return password == string(p), "test-client"
}

type waitingHysteriaAuthenticator struct {
	entered chan struct{}
	release chan struct{}
}

func (a waitingHysteriaAuthenticator) Authenticate(net.Addr, string, uint64) (ok bool, id string) {
	close(a.entered)
	<-a.release
	return false, ""
}

type hysteriaTestOutbound struct {
	ctx     context.Context
	address string
}

func (o hysteriaTestOutbound) TCP(_ string) (net.Conn, error) {
	return (&net.Dialer{Timeout: time.Second}).DialContext(o.ctx, "tcp", o.address)
}

func (hysteriaTestOutbound) UDP(string) (server.UDPConn, error) { return nil, net.ErrClosed }
func (hysteriaTestOutbound) CheckUDP(string) error              { return net.ErrClosed }
