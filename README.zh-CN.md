# Aris MaaS

[English](README.md) | **简体中文**

面向个人大模型使用场景的 **MaaS 模型服务聚合网关 + 使用数据沉淀平台**。

Aris 将多个 MaaS 平台、多种模型和多种 API 协议统一接入，通过模型别名、上游端点管理、协议转换、鉴权、限流、审计和可视化管理，客户端只需面向统一网关地址和模型别名发起请求，无需感知供应商、真实模型名和上游接口差异。

## 核心能力

- **模型聚合与统一入口**：Endpoint/Model 两级配置（用户级多租户，普通用户管理自己的端点与模型，admin 可按用户过滤与代建），模型别名屏蔽上游差异，一个别名可关联多个 Endpoint；同时提供 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 兼容入口，并支持 OpenAI 与 Anthropic 之间的跨协议转换（含 SSE 流式事件）。
- **数据沉淀与治理**：模型交互聚合为 Session，Message/Tool 以协议无关结构存储并按 checksum 内容寻址去重；支持评分、分享链接、会话筛选与 ShareGPT NDJSON 流式导出（含图片内联），为私有模型 SFT 沉淀数据资产。
- **审计与运行监控**：每次调用记录 Token 四类用量、首 Token 延迟、流式时长、上游状态码和 Trace ID；提供趋势/成功率/吞吐/用量统计、Cron 审计，以及跨 Pod 的 QPS、CPU、内存、协程和网络收发监控。
- **订阅账号 OAuth 接入（Claude Code / Codex）**：订阅账号以独立的端点类型（`oauth_anthropic` / `oauth_openai`）经官方 OAuth 授权接入。access token 自动刷新（请求路径 3 分钟提前量 + 维护 Cron），凭据以 AES-256-GCM 密文存储；熔断键使用端点级稳定身份，token 轮换不会重置熔断状态。模型别名像普通端点一样绑定——调度、fallback、审计、计价与数据治理零改动复用；订阅窗口用量（Claude 5h/7d、Codex primary/secondary）由 Cron 探测并在管理端展示。
- **客户端配置导出**：`aris model export` 从服务端启用模型列表生成 OpenCode、Claude Code、Codex、Pi、DSH 的接入配置，幂等 patch、`.bak` 备份、原子替换、凭证文件 `0600`。
- **安全与治理**：GitHub/Google OAuth2 登录 + JWT 管理鉴权，Proxy API Key 调用鉴权，`pending`/`demo`/`user`/`admin` 四级权限与用户审核（demo 演示账户：模块白名单 + 会话白名单/全量脱敏双视角 + 一键登录 + 访问审计），Aho-Corasick 触发词拦截，Redis 令牌桶请求限流与 Token 用量限流。

## 同类项目横向对比

对比四个主流 MaaS 开源项目（快照 **2026-10-19**）：[new-api](https://github.com/QuantumNous/new-api)（Go/Gin + React；AGPLv3 + 第 7 条附加条款）、[LiteLLM](https://github.com/BerriAI/litellm)（Python + React；MIT + 商业企业授权）、[OmniRoute](https://github.com/diegosouzapw/OmniRoute)（Node/TypeScript + SQLite；MIT）、[sub2api](https://github.com/Wei-Shaw/sub2api)（Go/Gin + Vue；LGPL-3.0）。只对比技术侧能力，不含产品与运营侧功能。

三点结构性差异：

- **对话正文是数据模型选择，不是路线图排期。** Aris 落库，因此内容寻址去重、评分与 ShareGPT/SFT 导出是结构性能力；其余四家没有会话聚合——LiteLLM 可显式开启，但数据仍是 spend-log 形态，其数据集导出面向评测。
- **「配额强控」与「成本估算」是两种设计。** new-api 与 sub2api 走配额/余额扣减（预扣 → 结算、窗口软上限）；LiteLLM 的预算族依赖数据库，**无 DB 时 fail open**；Aris 只做成本估算，没有预算闸门。

标记：✅ 有 · ◐ 部分 · — 无。评分依据各项目在快照时点的自身 README/官方文档；带编号的单元格见下方[说明](#说明)。

| 维度 | Aris | new-api | LiteLLM | OmniRoute | sub2api |
| --- | --- | --- | --- | --- | --- |
| **协议入口覆盖** | ◐[1] | ✅[1] | ✅[1] | ✅ | ◐[1] |
| **跨协议转换** | ✅ | ✅[2] | ✅ | ✅ | ◐[2] |
| **上游渠道类型（API Key / 订阅 OAuth）** | ✅[3] | ◐[3] | ✅[3] | ✅[3] | ✅[3] |
| **模型别名与多端点绑定** | ✅ | ✅ | ✅ | ✅ | ◐[4] |
| **路由与调度** | ✅[5] | ✅[5] | ✅[5] | ✅[5] | ✅[5] |
| **熔断 / 隔离 / 降级** | ✅[6] | ◐[6] | ◐[6] | ✅[6] | ✅[6] |
| **多租户与权限** | ✅ | ✅ | ✅ | —[7] | ✅ |
| **认证与登录** | ✅[8] | ✅ | ✅[8] | ◐[8] | ✅ |
| **限流与并发控制** | ✅[9] | ✅ | ✅[9] | ✅ | ✅ |
| **用量计量与配额强控** | ◐[10] | ✅[10] | ✅[10] | ◐[10] | ✅[10] |
| **调用审计与用量统计** | ✅[11] | ✅[11] | ✅ | ◐ | ◐[11] |
| **对话正文落库** | ✅[12] | —[12] | ◐[12] | — | — |
| **对话数据集导出（ShareGPT/SFT）** | ✅[13] | — | ◐[13] | — | — |
| **内容安全与脱敏** | ✅[14] | ◐[14] | ✅[14] | ✅ | ◐ |
| **可观测性与运行时监控** | ✅ | ◐[15] | ✅[15] | ◐[15] | ◐[15] |
| **Token 压缩** | ✅[16] | — | ◐[16] | ✅[16] | — |
| **MCP / A2A** | — | ◐[17] | ✅[17] | ✅[17] | ◐[17] |
| **部署与水平扩展** | ✅[18] | ✅[18] | ✅[18] | ◐[18] | ✅ |
| **扩展机制** | ◐[19] | ✅[19] | ✅[19] | ✅[19] | ✅[19] |

### 说明

**1 · 协议入口覆盖** — Aris ◐：仅文本协议（Chat/Responses/Messages + Decision 原生透传）。new-api：四种文本协议 + Realtime/Responses WebSocket + 媒体 + 任务插件路由，约 60 种渠道类型。LiteLLM：面最广——batches/files/fine-tuning/evals/realtime/videos/ocr/search/memory、Bedrock/Vertex 原生、MCP + A2A。sub2api ◐：面向订阅客户端（Responses 含 `/backend-api/codex/responses`、Messages、Chat、`/v1/systemone`、Antigravity）。

**2 · 跨协议转换** — Aris 为 Chat、Responses 和 Messages 提供 8 条转发路径（3 条原生、5 条转换），包括 Chat → Responses。转换覆盖 SSE 流式事件。new-api：RelayKit 转换四种文本协议，但提示协议特有工具/字段可能无法精确映射。LiteLLM：`/v1/messages` 覆盖全部支持的提供商，Rust 核心仅少量路由。sub2api ◐：按提供商定向转换（Anthropic → xAI Responses、Codex Responses ← Chat、Antigravity）。

**3 · 上游渠道类型** — Aris ✅：`api_key` + 订阅 OAuth 端点（`oauth_anthropic` / `oauth_openai`，AES-256-GCM，3 分钟提前量刷新），共用同一转发链路。new-api ◐：约 60 种 API-Key 渠道 + `ChannelTypeCodex = 57 "ChatGPT Subscription (Codex)"`（OAuth key JSON，仅 `/v1/responses`）；Claude Code OAuth 渠道（原 type 53）**已被回滚**。LiteLLM ✅：API Key + ChatGPT 订阅 device flow + Copilot 逐用户 OAuth，前者无 cost map 价（spend 恒为 $0），Anthropic Pro/Max 未文档化。OmniRoute ✅：四层 fallback（Subscription → API Key → Cheap → Free）+ PKCE 登录。sub2api ✅：OAuth 订阅账号池（Claude/Codex/Grok/Antigravity）+ 粘性会话。

**4 · 模型别名与多端点绑定** — sub2api ◐：Composite Groups 把模型解析到具体提供商；其余四家为一等公民的「别名 → N 端点」绑定（带优先级与权重）。

**5 · 路由与调度** — Aris：优先级分档 + 档内 A-Res 加权洗牌 + 会话亲和（TTL 5 分钟，fail-open）。new-api：加权随机 + 可拖拽优先级 + 渠道亲和 + `auto` 跨分组重试。LiteLLM：5 种策略（默认 `simple-shuffle`）+ 四类专用路由器 + Auto Router。OmniRoute：19 种 combo 策略 + 16 因子打分。sub2api：粘性会话 + 分组内选号。

**6 · 熔断 / 隔离 / 降级** — Aris ✅：重试循环外 `Guard`——按 `BaseURL\|hash(APIKey)` 或订阅端点稳定身份的三态熔断（60s 窗口，≥10 请求且 ≥50% 错误打开，30s → 半开 1 次探测）+ 按 Key 信号量（≤32、1s）→ 503 + `Retry-After`；429 不计入，SSE body 租约持续占槽并上报断流。new-api ◐：定时健康探测 + 自动禁用/恢复，无阈值熔断。LiteLLM ◐：仅 router cooldown。OmniRoute ✅：提供商熔断（仅 408/5xx，8×/12×/2× → 半开 60/30/15s）+ 连接冷却（×2 退避，遵循 `Retry-After`）+ 单模型锁死 + 过载排队。sub2api ✅：账号错误策略 + 临时不可调度 + failover。

**7 · 多租户与权限** — OmniRoute —：单运营者，仅 API-Key 作用域、IP 过滤与 33 个 MCP scope。Aris：用户级多租户。new-api：用户/分组/令牌 + 细粒度管理权限。LiteLLM：Organizations → Teams → Users → Virtual Keys（企业版组织/团队管理员）。sub2api：用户/分组 + 用户级与账号级并发。

**8 · 认证与登录** — Aris ✅：OAuth2 + JWT + API Key，**无 2FA 与 passkey**。LiteLLM ✅：SSO（OIDC/SAML，≤5 用户免费）+ JWT-OIDC 懒注册 + SCIM（企业版）+ 登录失败限流。OmniRoute ◐：密码登录 + 可选 OIDC 网关。

**9 · 限流与并发控制** — Aris ✅：按 IP/API Key 100 次/分钟；Token 维度中间件存在但 **TPM 刻意等效不限**（`LimitCallProxyLLMToken = 1e8`，源码注释为设计决定）。LiteLLM ✅：四级 RPM/TPM，`enforce_model_rate_limits` 升级为硬限制（429 + `retry-after: 60`）；多实例必须共享 Redis，否则按进程各算。

**10 · 用量计量与配额强控** — Aris ◐：按模型定价（微单位、时段窗口 + 上下文分档 + 缓存创建 5m/1h 分档、调用时刻快照），但**只估算不拦截**，配额是每 Key 请求数上限。new-api ✅：配额点数（1 USD = 500,000）预扣 → 结算 → 差额调整 + 分组/令牌配额 + 订阅额度与重置周期。LiteLLM ✅：预算族（Key/用户/团队/团队成员/Tag/全局），**依赖数据库——无 DB 时 `max_budget` fail open**。OmniRoute ◐：按 Key 的 USD 花费配额 + 成本分析（订阅类提供商显示 $0）。sub2api ✅：Token 级成本 + 余额/订阅扣减 + 5h/1d/7d 窗口（**请求后软上限**，在途可超出）。

**11 · 调用审计与用量统计** — Aris ✅：逐调用 `ModelCallAudit`（Token 四维、首 Token 与流式两段延迟、上游状态码、UA、Trace ID、估算费用），协程池异步落库。new-api ✅：使用/绘图/任务日志含 Token、费用、耗时与首字时间、请求 ID/上游请求 ID，管理员另有重试链、请求转换与计费路径。sub2api ◐：仅看板级请求/Token/费用与账号状态。

**12 · 对话正文落库** — Aris ✅：每次调用的完整历史落 Session 聚合，Message/Tool 按 checksum 去重、协议无关存储，同前缀快照实时合并（例外：触发词 `omit` 与 Playground）。new-api —：日志字段只有 Token/费用/延迟/ID，请求记录 prompt 的 issue #924/#2721/#2982/#4852 全部 closed。LiteLLM ◐：默认关闭，需 `store_prompts_in_spend_logs` 开启，另有 `turn_off_message_logging`、`no-log` 脱敏开关。

**13 · 对话数据集导出** — Aris ✅：ShareGPT NDJSON 流式导出（一会话一行，`X-Total-Sessions` 校验完整性），保留多轮对话、`<think>` 思维链、工具调用与内联图片，支持评分/模型/时间窗筛选与导出前统计预览。LiteLLM ◐：Lens datasets（Export JSONL）面向**评测**，需独立 Lens 服务 + ClickHouse，无 ShareGPT/SFT 格式。



**14 · 内容安全与脱敏** — Aris ✅：Aho-Corasick 触发词三动作（`deny`/`omit`/`capture`），Redis pub/sub + 版本轮询热更新。LiteLLM ✅：约 50 个 guardrail 集成、四种模式，但**流式输出上改写不生效**，且 `llmguard`/`llamaguard`/`hide_secrets`/`lakera` 需企业授权。new-api ◐：仅有渠道额外设置与上游 Header 覆盖。

**15 · 可观测性与运行时监控** — LiteLLM ✅：指标族最完整（TTFT、overhead、预算余量、准入/拒绝、数据库队列）、2 个官方 Grafana 面板，专用指标端口绕过虚拟 Key 鉴权（勿暴露公网）。new-api ◐：看板 + 健康探测 + Webhook 告警 + pprof/Pyroscope，仅自定义 `/api/perf-metrics`，**无 Prometheus 依赖**。sub2api ◐：管理台 + 渠道监控，`go.mod` 无 Prometheus 依赖。OmniRoute ◐：看板 + `X-OmniRoute-*` 头 + SQLite 审计轨迹，无 `/metrics`。

**16 · Token 压缩** — Aris ✅：转发前对 tool output 做确定性压缩，typed DTO 就地修改，策略 `smart_crusher`/`log_compressor`/`search_compressor`/`passthrough` 并记录压缩前后。OmniRoute ✅：12 引擎流水线（RTK、Caveman、LLMLingua-2、GCF v3.2、OmniGlyph）+ 默认开启膨胀保护。LiteLLM ◐：`compress()`（beta）。

**17 · MCP / A2A** — LiteLLM ✅：MCP Gateway（按 Key 的 server/tool 权限、MCP 成本追踪、`/mcp` 可被 Cursor 接入）+ A2A Agent Gateway。OmniRoute ✅：内置 MCP 服务端 110 工具（stdio/HTTP/SSE，33 scope）+ A2A v0.3。new-api ◐：仅 JS 任务插件 + 市场。sub2api ◐：仅 `hashicorp/go-plugin` 插件体系。

**18 · 部署与水平扩展** — Aris ✅：Compose/K8s（`preStop` + `terminationGracePeriodSeconds: 480`）+ 9 个 Fx 生命周期 Hook 排空请求、SSE 和协程池，跨 Pod 状态走 Redis。new-api ✅：多节点需共享主库 + 统一 `SESSION_SECRET`/`CRYPTO_SECRET`，共享限流需 Redis。LiteLLM ✅：Helm（HPA/KEDA）+ AWS/GCP 官方 Terraform（无 Azure）+ cosign 签名 + Prisma 迁移，多实例必须 Redis。OmniRoute ◐：单节点 SQLite，远程模式用作用域 token 驱动另一实例。

**19 · 扩展机制** — Aris ◐：无插件体系，扩展点是 models.dev 导入与 Fx 装配。new-api ✅：JS 任务插件（文档化 API + 市场）+ 自定义 OAuth 提供商。LiteLLM ✅：8 个 `CustomLogger` 挂载点 + 任意语言 guardrail API + 自定义认证/SSO/密钥管理 + 改一个 JSON 注册提供商 + `s3://`/`gcs://` 加载回调。OmniRoute ✅：插件框架、市场与 skills 框架。sub2api ✅：`hashicorp/go-plugin` 与模块化 skills。

## 系统架构

架构图按 MECE 分为五个互斥视角，每张图只回答一个问题；静态结构、同步主链、异步数据流和发布生命周期不再混画。

| 视角 | 图号 | 关注问题 |
| --- | --- | --- |
| 系统与接入 | 01–02 | 系统由什么组成，请求如何完成认证 |
| 代理与上游 | 03–05 | 调用如何转发，端点如何调度，订阅凭据如何维护 |
| 数据资产 | 06–08 | 会话与图片如何建模、落库并导出 |
| 治理与观测 | 09–12 | 权限、内容、安全和后台维护如何运行 |
| 交付与生命周期 | 13–16 | 前端如何运行和交付，服务如何发布与退出 |

### 系统与接入

**01 · 系统总览**：后端按 DDD 分层组织，17 个应用用例模块通过 `command` / `query` / `port` 依赖领域抽象，基础设施反向实现仓储接口；全局中间件为 Trace → Compress → Log → Recover → Metrics → Inflight → Guard → CORS → Locale，Fx 管理 9 个生命周期 Hook，6 个 Cron 负责后台维护。[交互 HTML](docs/diagrams/archify/aris-arch-01.html)

![系统总览 · DDD 分层](docs/diagrams/archify/aris-arch-01.gif)

**02 · 接入分区与认证**：路由分为 Web / CLI / Proxy / Ops 四区。Web 的 `publicGroup` 与 `jwtGroup` 平行，避免公开 OAuth2、刷新、Demo 登录和分享被 JWT 中间件误伤；CLI 与 LLM Proxy 分别注册但共用 `X-API-Key` 认证；Ops 仅开放探活、指标和安装脚本。本图只讲认证，权限与归属见 09。[交互 HTML](docs/diagrams/archify/aris-arch-02.html)

![接入分区与认证边界](docs/diagrams/archify/aris-arch-02.gif)

### 代理与上游

**03 · LLM 代理主链**：API Key 鉴权 → 请求与 Token 双层限流 → 触发词 → 候选解析 → tool output 压缩与跨协议转换 → Guard / 重试 / fallback → 上游。Chat、Responses 和 Messages 共支持 8 条转发路径（3 条原生、5 条转换），另有 Decision 原生转发。JSON / SSE 同步返回，会话与审计只在收尾移交异步任务。[交互 HTML](docs/diagrams/archify/aris-arch-03.html)

![LLM 代理转发核心链路](docs/diagrams/archify/aris-arch-03.gif)

**04 · 上游调度与容错**：别名按自有租户 → public 池 → 可选共享池解析，候选按 priority 分档、A-Res 加权和 5 分钟亲和排序。Guard 使用脱敏的稳定 key：API Key 端点为 `BaseURL|sha256(APIKey)前8位`，订阅端点为 `BaseURL|oauth:{endpointID}`。同端点指数退避后再跨端点 fallback；SSE 租约覆盖整个 body，慢流持续占槽、断流计入熔断。[交互 HTML](docs/diagrams/archify/aris-arch-04.html)

![上游调度与容错](docs/diagrams/archify/aris-arch-04.gif)

**05 · 订阅 OAuth 生命周期**：Claude Code / Codex 通过 PKCE + 一次性 state 绑定，verifier 仅保存在服务端，端点与 AES-256-GCM 凭据同事务创建。请求路径提前 3 分钟刷新 access token，维护 Cron 主动刷新并按 30 分钟节流探测额度；授权、刷新、探测与模型请求可共用 `OAUTH_SUBSCRIPTION_PROXY`。token 轮换不改变熔断身份，额度快照只展示、不参与调度。[交互 HTML](docs/diagrams/archify/aris-arch-05.html)

![订阅账号 OAuth 凭据生命周期](docs/diagrams/archify/aris-arch-05.gif)

### 数据资产

**06 · 会话与内容模型**：Session 以 `api_key_id` 鉴权归属，Message / Tool 按 checksum 全局去重，Blob 按原始字节 sha256 去重。续写合并在独立短事务中以 `FOR UPDATE` 保持会话 ID、评分和分享稳定，并精确失效缓存；ModelCallAudit 是独立的调用事实。[交互 HTML](docs/diagrams/archify/aris-arch-06.html)

![会话与内容数据模型](docs/diagrams/archify/aris-arch-06.gif)

**07 · 异步持久化**：`MessageStoreTask`、`ModelCallAuditTask`、`DemoAccessAuditTask` 共用有界 `Pond storePool`，但不共用持久化路径。会话事务完成引用清洗、图片外置、引用锁定和 checksum 去重，提交后再合并前缀；审计按终局结果记录 4 维 token、2 段延迟、上游状态与估算费用。[交互 HTML](docs/diagrams/archify/aris-arch-07.html)

![异步任务与持久化数据流](docs/diagrams/archify/aris-arch-07.gif)

**08 · 图片与数据集**：客户端自带的 `aris-blob:` 引用先降级为文本，合法 base64 图片由服务端生成 ImageRef，并经 pending / uploaded 状态写入 DB 与 S3 兼容对象存储。详情读取与数据集导出共用 ImageReader；导出按 API Key 归属过滤，一会话一行 NDJSON，图片恢复为 base64，`X-Total-Sessions` 用于完整性校验。[交互 HTML](docs/diagrams/archify/aris-arch-08.html)

![图片外置与数据集导出数据流](docs/diagrams/archify/aris-arch-08.gif)

### 治理与观测

**09 · 权限、归属与 Demo**：`pending < demo < user < admin`，写接口要求 user 及以上；普通用户按 UserID / APIKeyID 隔离，空 scope 必须返回空结果。public Endpoint 仅 admin 可维护。Demo 采用模块白名单 fail-closed、会话白名单与全量脱敏双视角，并记录登录及模块访问审计。[交互 HTML](docs/diagrams/archify/aris-arch-09.html)

![权限、归属与 Demo 治理](docs/diagrams/archify/aris-arch-09.gif)

**10 · 触发词热更新**：DB 变更通过 `Publish(trigger:changed)`、`INCR(trigger:version)`、2 秒轮询和 5 分钟兜底驱动各 Pod 全量重建 Aho-Corasick。`deny` 返回协议原生拦截响应，`omit` 只跳过会话存储，`capture` 保存此前上下文且不请求上游；混合命中优先级为 deny > capture > omit。[交互 HTML](docs/diagrams/archify/aris-arch-10.html)

![触发词热更新](docs/diagrams/archify/aris-arch-10.gif)

**11 · 日志、审计与运行时指标**：三条事实链彼此独立——结构化日志经 `X-Trace-Id` 进入 CLS，模型调用经 Pond 写入 PostgreSQL 审计，Prometheus Registry 每 5 秒快照到 Redis。Monitor 当前展示 QPS / CPU / 内存 / 协程 / 网络，`/metrics` 则由 `promhttp.Handler` 直读本 Pod Registry。[交互 HTML](docs/diagrams/archify/aris-arch-11.html)

![日志、审计与运行时指标](docs/diagrams/archify/aris-arch-11.gif)

**12 · 后台任务**：`CronManager` 支持热重载与手动触发，`cron:reload` 跨 Pod 同步，Redis 锁保证单实例执行并记录 `CronCallAudit`。当前 6 项任务为终态清理、软删清理、Think 提取、触发词命中回写、图片补传和 OAuth 维护；Session 前缀合并在每次存储提交后实时执行，不占 Cron。[交互 HTML](docs/diagrams/archify/aris-arch-12.html)

![定时任务触发与执行](docs/diagrams/archify/aris-arch-12.gif)

### 交付与生命周期

**13 · Web 前端运行时**：Next.js 静态导出，根布局包含 I18n / Theme / Auth、InteractionLayer 与首帧主题和动效脚本；当前为 15 个后台页面、4 个公开页面及 12 个共享 hooks。`src/lib` 统一后端访问，Playground 使用 SSE，Dataset 使用 NDJSON。[交互 HTML](docs/diagrams/archify/aris-arch-13.html)

![Web 管理后台前端架构](docs/diagrams/archify/aris-arch-13.gif)

**14 · 前端构建与内嵌交付**：`next build` 输出 `/web` 静态产物，`make web-build` 拷贝并逐文件 `gzip -9`，再由 `embed.FS` 随服务端二进制发布。`/web/*` 显式协商 gzip，静态资源未命中返回 404，页面路径回落 `index.html`；Maple Mono CN 字体由 Cloudflare R2 托管，不进入二进制。[交互 HTML](docs/diagrams/archify/aris-arch-14.html)

![前端构建与内嵌交付](docs/diagrams/archify/aris-arch-14.gif)

**15 · 部署与发布**：master / tag 构建前端与 Go 二进制，通过 buildx 发布 linux/amd64 GHCR 镜像；master 或手动触发按 digest 滚动部署并验收 `/health` 与 `/web/`。K8s 运行 2 副本，`maxUnavailable: 0`，资源 limit 为 512Mi / 1000m；tag 同时发布 aris 四平台客户端。[交互 HTML](docs/diagrams/archify/aris-arch-15.html)

![部署与发布拓扑](docs/diagrams/archify/aris-arch-15.gif)

**16 · 优雅退出**：Fx 逆序执行 9 个 Hook：Cron → Inflight → Pond → Metrics Flusher → HTTP → Logger → TriggerService → DB → Redis。Inflight 先于 Pond，确保被截断流仍能完成计量和存储；Pond 内部按 store → 图片上传 → agent 停止。K8s 以 `preStop sleep 10` 和 `terminationGracePeriodSeconds: 480` 配合滚动摘流。[交互 HTML](docs/diagrams/archify/aris-arch-16.html)

![优雅退出序列](docs/diagrams/archify/aris-arch-16.gif)

## 技术栈

| 层 | 技术 |
| --- | --- |
| 后端 | Go 1.25 · Fiber v3 · Huma v2 · Cobra · Viper · Uber Fx |
| 数据 | PostgreSQL + GORM · Redis（缓存/令牌桶/Cron 锁）· SQLite（单测）· Pond 协程池 |
| 可观测 | Zap + Lumberjack · 腾讯云 CLS · Prometheus · fgprof · `X-Trace-Id` 链路追踪 |
| 前端 | Next.js 16 (App Router) · React 19 · TypeScript · Tailwind CSS 4 · Base UI/shadcn · Recharts · Mermaid |
| 交付 | 前端静态产物 gzip 预压缩 + `embed.FS` 内嵌 · Docker 多阶段构建（Distroless nonroot）· Docker Compose · Kubernetes · GitHub Actions |

## 快速开始

### 环境要求

- Go 1.25+、Node.js 22+（仅前端开发/构建需要）
- PostgreSQL、Redis（可用 Docker Compose 启动）

### 启动依赖与配置

```bash
git clone https://github.com/hcd233/aris-mass.git
cd aris-mass
go mod download

# 准备配置（按需修改数据库、Redis、JWT Secret、OAuth2 等）
cp env/api.env.template env/api.env
cp env/postgresql.env.template env/postgresql.env
cp env/redis.env.template env/redis.env

# 启动 PostgreSQL 和 Redis
docker volume create postgresql-data && docker volume create redis-data
docker compose -f docker/docker-compose-full.yml up -d
```

### 迁移并启动服务

```bash
# 数据库迁移
go run ./cmd/server database migrate

# 构建前端并嵌入（生产模式必需；纯后端开发可跳过）
make web-build

# 启动服务
go run ./cmd/server server start --host localhost --port 8080
```

启动后访问：

- 管理后台 `http://localhost:8080/`
- 健康检查 `/health` · 就绪检查 `/ready` · SSE 健康检查 `/ssehealth`
- 开发环境 API 文档 `/docs`、OpenAPI `/openapi.json`（生产环境自动关闭）

首次使用：通过 OAuth2 登录管理后台 → 管理员审核用户 → 创建 Proxy API Key → 配置 Endpoint 和 Model → 即可用 OpenAI/Anthropic 兼容接口调用。

### 前端独立开发

```bash
cd web && npm ci && npm run dev   # http://localhost:3000
```

## 常用命令

| 命令 | 说明 |
| --- | --- |
| `make build` | 生产构建服务端 + 四平台 `aris` 客户端 |
| `make build-server` / `make build-client[-all]` | 分别构建服务端 / `aris` 客户端 |
| `make web-build` | 构建前端到 `internal/web/dist` 并 gzip 预压缩 |
| `make test` / `make test-cover` | 全量测试 / 覆盖率报告 |
| `make lint` | 自定义规范 lint（`lint conv`）+ 静态检查（vet/staticcheck/golangci-lint） |
| `make web-lint` / `make web-format` | 前端 ESLint / Prettier |
| `make fgprof` | 拉取远程 fgprof profile 并打开火焰图 |

服务端 CLI：`server start`、`database migrate`、`object migrate`（`cmd/server`）；规范与静态分析：`lint conv/static`（`cmd/lint`）；`aris` 客户端 CLI：`aris init`、`aris model export`、`aris status`、`aris update`（`cmd/client`）。

## API 概览

所有 API 由 Huma 注册并生成 OpenAPI，开发环境可在 `/docs` 交互查看。

| 前缀 | 认证 | 说明 |
| --- | --- | --- |
| `/api/openai/v1` · `/api/anthropic/v1` | `X-API-Key` | LLM 兼容入口：chat/completions、responses、messages、count_tokens、models |
| `/api/cli/v1/model/list` · `/aris/client/check` | `X-API-Key` | `aris` 客户端模型分发与客户端 Key 检查 |
| `/api/web/v1/oauth2` · `/token` | 公开（限流） | OAuth2 登录/回调、Token 刷新 |
| `/api/web/v1/user` | JWT | 个人资料；管理员可列表、审核（approve/demote）、删除用户 |
| `/api/web/v1/apikey` · `/session` · `/dataset` | JWT + Owner 隔离 | API Key、会话/分享、数据集导出 |
| `/api/web/v1/endpoint` · `/model` · `/upstream` | JWT（用户级多租户） | 端点、模型（用户隔离，admin 可按用户过滤与代建）、上游聚合视图 |
| `/api/web/v1/trigger` · `/cron` · `/audit/cron` · `/metrics` | JWT + admin | 触发词、Cron 管理/触发、Cron 审计、运行时指标 |
| `/api/web/v1/audit` | JWT | 模型调用审计与统计（管理员可见全局） |
| `/health` `/ready` `/ssehealth` `/metrics` `/install.sh` | 公开 | 探针、Prometheus 指标、`aris` 客户端安装脚本 |

## 部署

- **单机 Compose**：`docker compose -f docker/docker-compose-single.yml up -d`（需 `env/api.env`、`IMAGE_TAG` 和外部网络 `1panel-network`，迁移容器成功后 API 才启动，宿主机 `7070` → 容器 `8080`）；开发环境用 `docker-compose-dev-single.yml`（`7060` 端口）。
- **Kubernetes**：Deployment + Service + ConfigMap/Secret，滚动更新；`preStop` + draining + `terminationGracePeriodSeconds` 支持 SSE 长连接无损下线。
- **CI**：GitHub Actions 构建 GHCR 镜像并发布 `aris` 客户端 Release（tar.gz + sha256），服务端 `/install.sh` 提供自包含安装脚本。

## 项目结构

```text
cmd/server        服务端入口（start / database migrate / object migrate）
cmd/lint          规范与静态分析入口
cmd/client        aris 客户端入口
internal/
  router/         路由注册（Huma Operation + 中间件绑定）
  handler/        HTTP 适配层
  application/    用例编排（identity/session/llmproxy/...）
  domain/         领域模型与规则
  infrastructure/ GORM Repository、Redis、JWT、LLM Transport、OAuth、对象存储、协程池
  cron/           6 个维护任务（会话、Think、触发词、图片、OAuth）
  bootstrap/      Fx 依赖注入与生命周期
  web/dist        前端构建产物（embed.FS）
web/              Next.js 管理后台源码
  src/app/        App Router 路由组（(dashboard) 15 页 · 门户 / login / callback / share）
  src/components/ 基础与业务组件（ui / charts / chat / dataset / playground / motion）
  src/lib/        api-client · auth-context · i18n · theme · types（后端 DTO 镜像）
docker/           Dockerfile 与 Compose 配置
env/              环境变量模板
docs/             设计文档与架构图
test/             单元测试与 E2E 测试
```

## 文档

- 架构图集：[docs/diagrams/](docs/diagrams/)
- 设计与排障文档：[docs/](docs/)
- 开发规范（面向贡献者与 AI Agent）：[AGENTS.md](AGENTS.md) 与 [docs/agents/](docs/agents/)

## License

[Apache License 2.0](LICENSE)
