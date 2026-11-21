# 后端日志规范

以响应头 `X-Trace-Id` 的值在 CLS 中查询 `traceID:"<值>"`，按时间查看同次调用。日志使用现有 Zap 输出和 CLS 采集，消息保留 `[PascalCaseModule]` 前缀，新增稳定的 `event` 字段。耗时统一用整数毫秒，字段名使用 camelCase，延续现有 `traceID`、`userID` 和 `apiKeyID`。

## 事件

| event | 阶段 | 字段 |
| --- | --- | --- |
| `http.request.finished` | HTTP 响应准备完成 | 方法、匹配路由、HTTP 状态、业务错误码、耗时、是否为流、受控查询参数和请求摘要 |
| `llm.route.attempt` / `llm.route.fallback` / `llm.route.failed` | 端点选择、切换 | 请求模型、内部端点 ID 和名称、上游模型、候选顺序和下一端点 |
| `llm.upstream.request` / `llm.upstream.result` | 网关发起的每次 HTTP 客户端尝试 | 协议、上游操作、候选顺序、尝试序号、发送时参数、收到最终响应头的耗时、上游状态码、上游请求 ID 和可用的错误类型、码、参数名 |
| `llm.upstream.retry` / `llm.upstream.retry_canceled` | 同端点退避 | 尝试序号、最大尝试次数、退避毫秒、错误类别 |
| `llm.call.finished` | 代理使用量结算 | API 与上游协议、模型、端点、终态、首 token 延迟、流持续时间和四维 token 用量 |
| `llm.stream.finished` | SSE 写回调结束 | 耗时和错误类别，覆盖响应头已提交后的断流 |
| `llm.audit.persisted` / `llm.session.persisted` | 异步写入完成 | 结果、耗时及相关业务 ID |
| `db.statement.finished` | PostgreSQL 操作完成 | SQL 模板、行数、耗时、错误类别或 SQLSTATE |

正常请求与阶段完成使用 INFO；业务拒绝、可恢复失败和重试使用 WARN；服务端失败使用 ERROR。探活只采样成功事件。HTTP 200 的业务失败同时记录 `status=200` 和 `bizErrorCode`。入口日志覆盖业务中间件提前拒绝与恢复的 panic。Prometheus 抓取端点沿用独立路由。SSE 的 HTTP 日志表示响应准备完成，`llm.stream.finished` 报告写回调结果，`llm.call.finished` 报告上游业务终态，例如 HTTP 200 中的 `failed` 或 `incomplete`。HTTP 与 SSE 两条事件可能并行写入。反向代理或 fasthttp 在应用入口前拒绝的请求从对应入口日志排查。

## 参数和错误

- 只写白名单参数。推理参数包含模型、流开关、采样值、token 限额、推理档位、消息/输入/工具数量。查询只取有限的业务 ID、分页和枚举值。请求 JSON 摘要检查上限为 1 MiB，错误 JSON 上限为 8 KiB。超限、非 JSON、无效 JSON 或字段类型不匹配时，日志记录请求体积及摘要状态。单个机器标识最大 128 字节。
- 凭据、令牌、Cookie、OAuth 参数、提示词、模型输出、工具定义和参数、原始 query、任意 header 和原始错误体禁止进入本规范的事件。可检索的供应商错误字段限定为机器标识或参数路径，不采集自由文本。SQL 记录占位符和 SQLSTATE，避免记录参数值和数据库错误详情。
- HTTP 流不得为日志读取或消费响应体。异步任务使用已有的请求上下文副本，保留 traceID。上游业务流量、重试策略、审计数据和对客户端的响应契约按现有语义执行。

## 使用 CLS 定位问题

1. 从响应头获取 `X-Trace-Id`，用 `traceID:"<值>"` 查询。
2. 查看 `http.request.finished` 的 `status`、`bizErrorCode` 和 `requestParams`，确认请求入口与用户提交的参数。
3. 按 `endpointAttempt` 和 `upstreamAttempt` 对照端点选择和真实发送参数。读取 `llm.upstream.result` 事件的 `durationMs`、`upstreamStatusCode`、`upstreamErrorCode` 和 `upstreamRequestID`。
4. 结合模型调用、SSE 消费和异步持久化事件确认最终结果。旧日志可继续按 `traceID` 查询，并通过原有模块消息定位。
