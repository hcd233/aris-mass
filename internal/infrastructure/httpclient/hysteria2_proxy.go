package httpclient

import (
	"cmp"
	"context"
	"errors"
	"math"
	"net"
	"strconv"

	hyclient "github.com/apernet/hysteria/core/v2/client"
	hyerrors "github.com/apernet/hysteria/core/v2/errors"
	"github.com/apernet/hysteria/extras/v2/obfs"
	"github.com/bytedance/sonic"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/ierr"
)

type hysteria2Node struct {
	Host          string `json:"host"`
	Port          string `json:"port"`
	Password      string `json:"password"`
	Peer          string `json:"peer"`
	AllowInsecure int    `json:"allowInsecure"`
	Obfs          string `json:"obfs"`
	ObfsParam     string `json:"obfsParam"`
}

func newHysteria2Proxy(lifetime context.Context, raw string) (subscriptionDialer, error) {
	var node hysteria2Node
	if err := sonic.UnmarshalString(raw, &node); err != nil {
		return subscriptionDialer{}, ierr.New(ierr.ErrValidation, "invalid Hysteria2 JSON in OAUTH_SUBSCRIPTION_PROXY")
	}
	port, err := strconv.Atoi(node.Port)
	if err != nil || port < 1 || port > math.MaxUint16 || node.Host == "" || node.Password == "" {
		return subscriptionDialer{}, ierr.New(ierr.ErrValidation, "Hysteria2 proxy requires host, port and password")
	}
	if node.AllowInsecure != 0 && node.AllowInsecure != 1 {
		return subscriptionDialer{}, ierr.New(ierr.ErrValidation, "Hysteria2 allowInsecure must be 0 or 1")
	}
	var obfuscation []byte
	switch node.Obfs {
	case constant.SubscriptionProxySalamander:
		if len(node.ObfsParam) < constant.SubscriptionProxySalamanderMinKeyLength {
			return subscriptionDialer{}, ierr.New(ierr.ErrValidation, "Hysteria2 Salamander requires obfsParam of at least 4 bytes")
		}
		obfuscation = []byte(node.ObfsParam)
	case "":
	default:
		return subscriptionDialer{}, ierr.New(ierr.ErrValidation, "unsupported Hysteria2 obfuscation")
	}

	// 连接工厂由 Hysteria 在 TCP 拨号中同步调用。每次拨号持有 slot，
	// 因而工厂只读取当前拨号的 context，其他调用可以取消对 slot 的等待。
	var attempt *hysteria2PacketFactory
	configure := func() (*hyclient.Config, error) {
		addr, err := net.DefaultResolver.LookupIPAddr(attempt.ctx, node.Host)
		if err != nil || len(addr) == 0 {
			return nil, ierr.New(ierr.ErrProxyRequest, "cannot resolve Hysteria2 proxy host")
		}
		return &hyclient.Config{
			ConnFactory: attempt,
			ServerAddr:  &net.UDPAddr{IP: addr[0].IP, Zone: addr[0].Zone, Port: port},
			Auth:        node.Password,
			TLSConfig: hyclient.TLSConfig{
				ServerName:         cmp.Or(node.Peer, node.Host),
				InsecureSkipVerify: node.AllowInsecure == 1,
			},
			FastOpen: true,
		}, nil
	}
	proxy, err := hyclient.NewReconnectableClient(configure, nil, true)
	if err != nil {
		return subscriptionDialer{}, ierr.New(ierr.ErrInternal, "cannot initialize Hysteria2 subscription proxy")
	}

	slot := make(chan struct{}, 1)
	return subscriptionDialer{
		dial: func(ctx context.Context, _ string, addr string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, constant.HTTPDialTimeout)
			defer cancel()
			stopOnShutdown := context.AfterFunc(lifetime, cancel)
			defer stopOnShutdown()

			select {
			case slot <- struct{}{}:
				defer func() { <-slot }()
			case <-ctx.Done():
				return nil, ctx.Err()
			}

			attempt = &hysteria2PacketFactory{ctx: ctx, obfuscation: obfuscation}
			defer func() {
				attempt.finish()
				attempt = nil
			}()
			conn, err := proxy.TCP(addr)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, sanitizeHysteria2Error(err)
			}
			return &hysteria2Tunnel{Conn: conn}, nil
		},
		close: proxy.Close,
	}, nil
}

type hysteria2PacketFactory struct {
	ctx         context.Context
	obfuscation []byte
	stopClose   func() bool
}

func (f *hysteria2PacketFactory) New(_ net.Addr) (net.PacketConn, error) {
	var listen net.ListenConfig
	conn, err := listen.ListenPacket(f.ctx, constant.SubscriptionProxyUDP, constant.SubscriptionProxyUDPListenAddr)
	if err != nil {
		return nil, err
	}
	if len(f.obfuscation) > 0 {
		wrapped, err := obfs.WrapPacketConnSalamander(conn, f.obfuscation)
		if err != nil {
			_ = conn.Close() //nolint:errcheck // 返回封装错误。
			return nil, err
		}
		conn = wrapped
	}
	// 官方客户端的 HTTP/3 认证没有接收 context。关闭 socket 中断卡住的认证，
	// 拨号结束后解除这次请求的取消关联，保留共享的 QUIC 会话。
	f.stopClose = context.AfterFunc(f.ctx, func() {
		_ = conn.Close() //nolint:errcheck // 中断认证。
	})
	return conn, nil
}

func (f *hysteria2PacketFactory) finish() {
	if f.stopClose != nil {
		f.stopClose()
		f.stopClose = nil
	}
	f.ctx = nil
}

type hysteria2Tunnel struct{ net.Conn }

func (c *hysteria2Tunnel) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	return n, sanitizeHysteria2Error(err)
}

func sanitizeHysteria2Error(err error) error {
	var rejected hyerrors.DialError
	if errors.As(err, &rejected) {
		return ierr.New(ierr.ErrProxyRequest, "Hysteria2 proxy rejected the upstream connection")
	}
	return err
}
