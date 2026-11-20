package httpclient

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/ierr"
	_ "github.com/xtls/xray-core/app/dispatcher" // 注册 Xray 出站分发器。
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/transport/internet"
	_ "github.com/xtls/xray-core/transport/internet/headers/http" // 支持 VMess/VLESS 的 TCP HTTP 头。
	_ "github.com/xtls/xray-core/transport/internet/tagged/taggedimpl"
)

func newXrayProxy(ctx context.Context, raw string) (subscriptionDialer, error) {
	var outbound conf.OutboundDetourConfig
	if err := sonic.UnmarshalString(raw, &outbound); err != nil {
		return subscriptionDialer{}, ierr.New(ierr.ErrValidation, "invalid Xray outbound JSON in OAUTH_SUBSCRIPTION_PROXY")
	}
	if err := validateXrayOutbound(&outbound); err != nil {
		return subscriptionDialer{}, err
	}

	xrayConfig := conf.Config{
		LogConfig:       &conf.LogConfig{LogLevel: constant.SubscriptionProxyLogDisabled},
		OutboundConfigs: []conf.OutboundDetourConfig{outbound},
	}
	built, err := xrayConfig.Build()
	if err != nil {
		// Xray 的解析错误可能包含配置字段原值。
		return subscriptionDialer{}, ierr.New(ierr.ErrValidation, "invalid Xray outbound settings in OAUTH_SUBSCRIPTION_PROXY")
	}
	installXraySocketCancellation()
	instance, err := core.New(built)
	if err != nil {
		return subscriptionDialer{}, ierr.New(ierr.ErrValidation, "cannot initialize Xray outbound in OAUTH_SUBSCRIPTION_PROXY")
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close() //nolint:errcheck // 初始化失败已用安全摘要报告。
		return subscriptionDialer{}, ierr.New(ierr.ErrInternal, "cannot start subscription proxy")
	}
	return subscriptionDialer{
		dial: func(requestCtx context.Context, _ string, addr string) (net.Conn, error) {
			return dialXray(requestCtx, ctx, instance, addr)
		},
		close: instance.Close,
	}, nil
}

func validateXrayOutbound(outbound *conf.OutboundDetourConfig) error {
	switch outbound.Protocol {
	case constant.SubscriptionProxyVMess, constant.SubscriptionProxyVLESS, constant.SubscriptionProxyTrojan, constant.SubscriptionProxyShadowsocks:
	default:
		return ierr.New(ierr.ErrValidation, "unsupported Xray protocol in OAUTH_SUBSCRIPTION_PROXY")
	}

	stream := outbound.StreamSetting
	if outbound.ProxySettings != nil || (stream != nil && stream.SocketSettings != nil && stream.SocketSettings.DialerProxy != "") {
		return ierr.New(ierr.ErrValidation, "OAUTH_SUBSCRIPTION_PROXY accepts a single Xray outbound without chaining")
	}
	if stream != nil && stream.Network != nil {
		switch *stream.Network {
		case constant.SubscriptionProxyRAW, constant.SubscriptionProxyTCP, constant.SubscriptionProxyWS, constant.SubscriptionProxyWebSocket:
		default:
			return ierr.New(ierr.ErrValidation, "OAUTH_SUBSCRIPTION_PROXY supports Xray TCP and WebSocket transports")
		}
	}
	return nil
}

func dialXray(ctx, lifetime context.Context, instance *core.Instance, addr string) (net.Conn, error) {
	destination, err := xnet.ParseDestination(constant.SubscriptionProxyTCPPrefix + addr)
	if err != nil {
		return nil, ierr.Wrap(ierr.ErrProxyRequest, err, "parse subscription proxy destination")
	}

	tunnelCtx, cancel := context.WithCancel(context.WithValue(ctx, xraySocketContextKey{}, true))
	conn, err := core.Dial(tunnelCtx, instance, destination)
	if err != nil {
		cancel()
		return nil, err
	}
	closeConn := sync.OnceValue(func() error {
		cancel()
		return conn.Close()
	})
	stop := context.AfterFunc(lifetime, func() {
		_ = closeConn() //nolint:errcheck // 关闭信号优先，连接已不可复用。
	})
	// core.Dial 立即返回 pipe。首个入站字节前，真实的代理连接可能还在握手。
	timer := time.AfterFunc(constant.HTTPDialTimeout, func() {
		_ = closeConn() //nolint:errcheck // 关闭超时的隧道。
	})
	return &xrayTunnel{Conn: conn, stop: stop, timer: timer, close: closeConn}, nil
}

type xrayTunnel struct {
	net.Conn
	stop  func() bool
	timer *time.Timer
	close func() error
}

func (c *xrayTunnel) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.timer.Stop()
	}
	return n, err
}

func (c *xrayTunnel) Close() error {
	c.stop()
	c.timer.Stop()
	return c.close()
}

type xraySocketContextKey struct{}

var installXraySocketCancellation = sync.OnceFunc(func() {
	internet.UseAlternativeSystemDialer(&xraySystemDialer{})
})

type xraySystemDialer struct {
	internet.DefaultSystemDialer
}

func (d *xraySystemDialer) Dial(ctx context.Context, source xnet.Address, dest xnet.Destination, options *internet.SocketConfig) (net.Conn, error) {
	conn, err := d.DefaultSystemDialer.Dial(ctx, source, dest, options)
	if err == nil && ctx.Value(xraySocketContextKey{}) != nil {
		context.AfterFunc(ctx, func() {
			_ = conn.Close() //nolint:errcheck // 关闭代理底层 socket 以中断握手。
		})
	}
	return conn, err
}
