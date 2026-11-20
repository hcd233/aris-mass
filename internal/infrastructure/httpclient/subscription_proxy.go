package httpclient

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/ierr"
)

type subscriptionDialer struct {
	dial  func(ctx context.Context, network, addr string) (net.Conn, error)
	close func() error
}

type subscriptionProxyFormat struct {
	Type string `json:"type"`
}

// InitSubscriptionProxy 在通用 HTTP 客户端初始化后配置订阅代理。
func InitSubscriptionProxy(raw string) (func() error, error) {
	// 使用独立连接池，沿用通用客户端的上游 TLS 和请求超时。
	transport := client.Transport.(*http.Transport).Clone()
	proxied := *client
	proxied.Transport = transport

	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, constant.SubscriptionProxyJSONPrefix) {
		route, err := parseSubscriptionProxyURL(raw)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(route)
		subscriptionClient = &proxied
		return func() error {
			transport.CloseIdleConnections()
			return nil
		}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	dialer, err := newSubscriptionDialer(ctx, raw)
	if err != nil {
		cancel()
		return nil, err
	}
	transport.DialContext = dialer.dial
	subscriptionClient = &proxied
	return func() error {
		cancel()
		transport.CloseIdleConnections()
		return dialer.close()
	}, nil
}

func newSubscriptionDialer(ctx context.Context, raw string) (subscriptionDialer, error) {
	var format subscriptionProxyFormat
	if err := sonic.UnmarshalString(raw, &format); err != nil {
		return subscriptionDialer{}, ierr.New(ierr.ErrValidation, "invalid proxy JSON in OAUTH_SUBSCRIPTION_PROXY")
	}
	if strings.EqualFold(format.Type, constant.SubscriptionProxyHysteria2) {
		return newHysteria2Proxy(ctx, raw)
	}
	return newXrayProxy(ctx, raw)
}

func parseSubscriptionProxyURL(raw string) (*url.URL, error) {
	route, err := url.Parse(raw)
	if err != nil || route.Hostname() == "" {
		return nil, ierr.New(ierr.ErrValidation, "OAUTH_SUBSCRIPTION_PROXY requires a proxy URL or JSON")
	}
	switch route.Scheme {
	case constant.SubscriptionProxyHTTP, constant.SubscriptionProxyHTTPS, constant.SubscriptionProxySOCKS5, constant.SubscriptionProxySOCKS5H:
		return route, nil
	default:
		return nil, ierr.New(ierr.ErrValidation, "unsupported proxy URL scheme in OAUTH_SUBSCRIPTION_PROXY")
	}
}
