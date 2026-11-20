# 订阅代理出口实施计划

> 使用 Superpowers 的 executing-plans 在当前会话实施。

通过一个环境变量，为现有和新建的 Codex 与 Claude 订阅端点指定服务端代理出口。

`OAUTH_SUBSCRIPTION_PROXY` 接收单个代理配置。Hysteria2 使用客户端导出的节点字段，VMess、VLESS 等使用 Xray outbound 对象，HTTP 和 SOCKS5 使用代理 URL。Aris 启动时初始化代理客户端。订阅 OAuth 客户端和模型 transport 根据端点认证类型使用同一个 HTTP 客户端。空配置沿用现有直连，已配置的出口错误按订阅上游错误返回。

相关设计见[订阅账号池化设计](../specs/2026-10-18-oauth-subscription-pooling-design.md)。

- [x] 用进程内 HTTP 代理写端到端测试，由真实启动入口加载环境变量，断言两家订阅的 token 兑换、刷新、额度查询和模型请求都通过代理，普通 API Key 模型请求直连。
- [x] 在 `internal/config` 加载环境变量，在 `internal/infrastructure/httpclient` 添加订阅客户端和生命周期清理。接受 Hysteria2、VMess、VLESS、Trojan、Shadowsocks、SOCKS5 和 HTTP 代理。初始化错误仅返回安全摘要。
- [x] 将 `internal/infrastructure/oauthprovider` 和 `internal/infrastructure/transport` 的请求接入订阅客户端，保持现有超时、流式转发和备用端点语义。
- [x] 更新环境变量模板与设计文档。通过本地代理验证配置校验、流式响应、失败时不直连、三种原生协议以及握手中关闭服务。全量 Go 测试、代理用例竞态检查、lint 及 Linux/amd64 构建通过。
- [x] 对提供的 Hysteria2 节点发起无账号凭据探测。当前环境下 QUIC 握手超时，未收到上游 HTTP 响应。
