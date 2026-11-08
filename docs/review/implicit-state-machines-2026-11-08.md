# aris-mass 三端隐式状态机扫描报告

> 扫描基线：`master` @ `f2e8a0de`（2026-11-08）
> 扫描范围：API 端 `internal/`（930 个 Go 文件）、Web 端 `web/src/` 全部 TS/TSX、CLI 端 `cmd/client/` + `internal/client/`（25 个 Go 文件）
> 判定标准：**存在可辨识的状态集合、事件与转移，但状态没有收敛到一个显式状态类型 / 转移函数里**。仅有枚举但无守卫的，记为「半隐式」。

---

## 0. 结论速览

三端共识别 **36 个隐式（含半隐式）状态机**：API 16、Web 10、CLI 10。

系统性问题集中在三类，而不是单个 bug：

1. **状态载体分裂**：同一个业务状态同时存在于 DB 列、Redis 缓存、进程内 map、文件系统与请求 `context` 里，各自有不同的失效时机。典型：API Key 吊销（DB 已删、Redis 鉴权缓存仍活 30s）、JWT 权限缓存（命令层主动失效、admin 提升却无接口）、DSH 双文件（凭证先写、patch 后写）。
2. **转移守卫散落**：合法转移写在 command/handler 的 `if` 里，聚合根只做字段赋值（`User.ChangePermission` 不校验来源态），导致「哪些转移合法」只能靠通读调用方得知。典型：权限生命周期、Endpoint 可见性、cron 启停。
3. **中间态不可见**：异步/流式/分布式流程的中间态没有落点，失败后只能靠日志推断。典型：会话快照合并（best-effort，失败等下轮自愈）、审计异步落库（队列满即丢）、`aris model export` 三处原子写语义不一致。

最值得优先处理的 7 个风险：

| # | 位置 | 问题 |
|---|---|---|
| 1 | 权限生命周期 | `admin` 无 API 可达路径，只能改库；改库不会失效 `jwt:user:{id}` 缓存 |
| 2 | OAuth 订阅凭据 | 刷新失败与凭据失效边界靠 `invalid_grant` 单点判定，缓存删除是「补丁式」正确性 |
| 3 | 会话续写合并 | 组锁 + 前缀算法的正确性依赖「插入后立即合并」的时序，失败仅记日志 |
| 4 | Web 准入与回调 | `harness` / `profile` 页无 `PermissionGuard` 可直达；OAuth 回调 `useEffect` 无幂等守卫，StrictMode / 语言切换会重复兑换一次性 code |
| 5 | Web 会话详情 | `?id=` 变化不重挂载，消息列表从不 `reset()` → 头部是新会话、正文是旧会话消息 |
| 6 | CLI 配置写盘 | `config` / `model` / `update` 三套原子写，只有 `update` 做了 `Sync` + 父目录 fsync |
| 7 | CLI `init` | 健康检查与 Key 校验的「重试」只重试同一个值，没有「返回修改」转移，可形成死循环 |

---

## 1. API 端（`internal/`）

### A1. 用户权限生命周期（pending / demo / user / admin）

**业务语义**：OAuth 注册后进入待审，管理员审核后可用；可被设为全局唯一 Demo；可降级回待审。权限决定全部管理接口与 demo 模块白名单。

**状态**：`pending(1) < demo(2) < user(3) < admin(4)`（`internal/common/enum/permission.go:10-24`，等级映射 `:33-41`）。

**转移表**

| 当前 | 事件 | 守卫 | 下一 | 副作用 | 证据 |
|---|---|---|---|---|---|
| — | OAuth 首次登录注册 | 名字非空 | `pending` | 写三方 bind id | `domain/identity/aggregate/user.go:44-63` |
| `pending` | admin 审核通过 | 目标必须 `pending` | `user` | 失效 JWT 用户缓存 | `application/identity/command/approve_user.go:58-70` |
| `user` | admin 降级 | 目标必须 `user`，禁止自降 | `pending` | 失效缓存 | `application/identity/command/demote_user.go:60-80` |
| `pending` \| `user` | admin 设为 Demo | 非自己；目标非 admin/非已 demo | `demo` | 旧 Demo 回 `pending`；两者缓存失效 | `command/set_demo_user.go:50-74`；`repository/user_repository.go:243-266` |
| `demo` | admin 恢复 | 目标必须 `demo` | `user` | 失效缓存 | `command/restore_demo_user.go:52-66` |
| 任意非 admin | admin 删除 | 非自己、非 admin | 软删 + 级联软删 API Key | 失效缓存 | `command/delete_user.go:52-80`；`user_repository.go:326-337` |
| 任意 | 直接改库 | 无 | `admin` | **无缓存失效** | 无代码路径 |

**隐式点**

- 聚合根 `User.ChangePermission`（`aggregate/user.go:126-137`）只做「相同则 return，否则赋值」，不校验来源状态；合法转移表完全由 5 个 command 的 `if` 拼出来。
- `admin` 是**外部注入态**：全仓没有任何 `ChangePermission(enum.PermissionAdmin)` 的调用点，也没有对应路由（`internal/router/user.go` 只有 approve/demote/delete/set-demo/restore-demo）。运维直接改库时，`jwt:user:{id}` 缓存（`middleware/jwt.go:88-119`，TTL = access token 有效期）不会失效，最长一个 access token 周期内仍按旧权限放行。
- Demo 单例不变量由「事务内先降旧、再升新」+ 部分唯一索引保证（`user_repository.go:247-266`），代码里没有 `demo` 唯一性的领域不变量。

**风险**

- 改库提权 / 降权不生效（缓存窗口内）。
- `ChangePermission` 允许任意 `enum.Permission` 入参，新增调用方时容易越级。
- 删除用户只级联 API Key，不处理其 Endpoint/Model 归属（需另行确认是否有意为之）。

---

### A2. Demo 账户：入口开关 + 模块白名单

**业务语义**：全局单例只读账户，登录入口可开关；可开放 8 个模块（dashboard/sessions/audit/upstream/trigger/monitor/cron/cron_audit）。

**状态**

- 账户态：`无 Demo` → `Demo 存在`（替换时旧 Demo → pending）。
- 入口态：`loginEnabled=true/false`。
- 模块态：每个模块 `开放/关闭`，读取失败按关闭（fail-closed）。
- 每次访问产生 `login / login_denied / module_access / module_denied` 四种审计动作（`enum/demo_access_action.go`）。

**转移**

| 当前 | 事件 | 守卫 | 下一 | 证据 |
|---|---|---|---|---|
| 无 Demo | admin 设某用户为 Demo | 目标 pending/user | Demo 存在 | `set_demo_user.go:50-74` |
| Demo 存在 | admin 设另一用户 | 同上 | 旧 Demo→pending，新→demo | `user_repository.go:247-266` |
| 入口关 | `POST /demo/login` | — | 拒绝 + `login_denied` 审计 | `demo/command/login.go:92-96` |
| 入口开 | `POST /demo/login` | 必须存在 Demo 用户 | 签发 token pair + `login` 审计 | `demo/command/login.go:98-132` |
| 模块关 | demo 访问该模块路由 | `demoModule != ""` | 403 + `module_denied` 审计 | `middleware/permission.go:84-108` |
| 模块开 | 同上 | accessor 读配置成功 | 放行 + `module_access` 审计 | 同上 |

**隐式点**：账户态、入口态、模块态分属三张表/配置，但「demo 是否可登录」是三者组合出的派生状态，没有任何地方统一表达；`DemoModuleAccessor` 每次请求读一次配置（`demo/query/get_config.go:77-85`），读失败即整体关闭。

**风险**

- 配置读取失败会让所有 demo 模块瞬间 403，排查时容易被误判为权限 bug（日志有 fail-closed 标记，可缓解）。
- `CONTEXT.md` 描述的 `sampleModulus`（按 `id % K` 抽样）在当前代码中**不存在**，`demo_configs` 只有 `login_enabled/modules`（`model/demo_config.go:13-22`），demo 会话改由 `demo_sessions` 白名单控制（`model/demo_session.go`）。文档与实现已漂移。
- `CONTEXT.md` 的模块清单仍写 `models/endpoints`，代码与前端已合并为 `upstream`（`enum/demo_module.go`、`web/src/lib/demo-modules.ts:14-18`）。

---

### A3. 订阅凭据与 access token 有效期

**业务语义**：Claude Code / Codex 订阅账号的 OAuth 凭据，active 时可刷新并转发；上游明确拒绝刷新（`invalid_grant`）后转 `reauth_required`，端点从候选池剔除。

**状态**

`active` → `reauth_required` →（重新授权）`active`（`enum/oauth_credential_status.go`）。

叠加一个 token 新鲜度子状态机：`fresh`（剩余 > 3min）/ `stale`（≤3min）/ `refreshing`（他方持锁）/ `expired`。

**转移**

| 当前 | 事件 | 守卫 | 下一 | 证据 |
|---|---|---|---|---|
| `active` | 请求发现临期（≤3min） | 拿到刷新锁 | 刷新成功 → `active` + 写缓存 | `oauthprovider/token_provider.go:58-63, 110-159` |
| `active` | 请求发现临期 | 锁被他方持有 | 用缓存 / 未真过期旧 token / 返回可切换错误 | `token_provider.go:100-109` |
| `active` | 刷新返回 `invalid_grant` | 仅认该错误码 | `reauth_required` + 删 access 缓存 | `token_provider.go:138-149` |
| `active` | 刷新网络/5xx 失败 | — | 状态不变，等 cron 重试 | `token_provider.go:150-152` |
| `reauth_required` | 重新授权回贴 code | flow 校验通过 | `active` + 清缓存 | `endpoint/command/oauth_reauth.go:98-109` |
| `reauth_required` | 请求解析候选 | — | 端点被剔除（fail-closed） | `domain/llmproxy/service/resolver.go:168-172` |

**隐式点**：状态字段在聚合根上，但「什么时候该刷新」「刷新失败算不算状态转移」分散在 `token_provider`；`GetAccessToken` 的四个兜底分支（锁故障 / 锁竞争 / 缓存命中 / DB 过期）共同定义了一个未命名的状态机。缓存删除必须与状态标记成对出现，正确性依赖调用方自觉（`oauth_reauth.go:107-109` 与 `token_provider.go:142-144` 各写一次）。

**风险**

- 状态标记与落库是两步：`MarkReauth` 后先删缓存、再 `Save`（`token_provider.go:141-148`）；若 `Save` 失败且进程退出，DB 仍是 `active`，下个请求会再次刷新、再次失败，直到持久化成功（不会误用死 token，但会反复打上游）。
- `OAuthAccessCache` 的 TTL 由剩余有效期推导（`token_provider.go:163-169`），若上游提前吊销 access token，本系统无法感知，只能等 401/429 触发 fallback。

---

### A4. OAuth 授权流（一次性 state）

**业务语义**：发起授权时生成一次性记录，用户回贴 code 后兑换；即取即删防重放。

**状态**：`issued`（Redis `oauth:flow:{state}`，TTL 10min）→ `consumed` / `expired`。

**转移**

| 当前 | 事件 | 守卫 | 下一 | 证据 |
|---|---|---|---|---|
| — | 发起授权 | 生成 state + PKCE | `issued` | `endpoint/command/oauth_authorize.go` |
| `issued` | 回贴 code 兑换 | `Take` 命中 + 用户/Provider 匹配 | `consumed`（删除） | `oauth_exchange.go:53-65`；`oauth_reauth.go:62-74` |
| `issued` | 回贴 code | `Take` 未命中（过期/已用） | 报错，要求重新发起 | 同上 |
| `issued` | 超时 | TTL 到期 | `expired` | `constant/oauth_subscription.go:61-62` |

**隐式点**：没有 `OAuthFlow` 状态字段，`issued/expired` 靠 Redis key 是否存在推导；`consumed` 与 `expired` 对调用方不可区分（都是 `!ok`），错误文案统一为「not found or expired」。

**风险**：兑换失败（例如上游网络错误）时 flow 已被 `Take`，用户必须回到管理页重新发起；这是有意的防重放取舍，但前端没有对应提示分支。

---

### A5. 端点解析与跨端点 fallback（请求级状态机）

**业务语义**：一次请求按 alias 解析出有序候选，逐个尝试；失败且可切换时换下一个端点。

**状态（单次请求）**：`resolving` → `candidate[i]` →（成功）`delivered` /（可切换）`next` /（不可切换或耗尽）`failed`；流一旦开始写出即进入终态，不再切换。

**转移**

| 当前 | 事件 | 守卫 | 下一 | 证据 |
|---|---|---|---|---|
| resolving | 自有租户命中 | — | 候选列表 | `resolver.go:129-151` |
| resolving | 自有未命中 | 无条件 | 公开池 | `resolver.go:135-141` |
| resolving | 公开池未命中 | `shared_pool_fallback` | 存量共享池 | `resolver.go:142-148` |
| resolving | 逐模型过滤 | enabled + OAuth active + matcher | 候选 | `resolver.go:153-177` |
| resolving | 排序 | priority 升序 + 档内 A-Res | 有序候选 | `resolver.go:182-193` |
| candidate[i] | 失败 | `CanSwitchEndpoint` 且非末位 且 ctx 未取消 | candidate[i+1] | `usecase/common.go:253-278` |
| candidate[i] | 失败 | 末位 / 取消 / 不可切换 | failed | 同上 |
| candidate[i] | 亲和命中 | key 非空 | 置顶 | `resolver.go:70-85`；`usecase/affinity.go:28-51` |

**隐式点**：fallback 的「当前尝试到第几个」「中间失败是否已提交审计」是函数局部变量 + `context` 值（`CtxKeyFailureAuditDeferral`，`constant/ctx.go:58-60`），没有请求级状态对象；亲和写回（`rememberAffinity`）失败即静默，Redis 映射与实际端点可能短暂不一致。

**风险**

- `runWithFallback` 的 `deferral.active=false` 在返回成功后执行（`common.go:258-259`），被切换掉的中间失败审计一律丢弃（设计如此）；若中间那次上游已计费，平台成本与审计记录会不一致。
- 亲和 TTL 5 分钟，换端点后改写；如果改写失败，后续请求会继续命中旧端点，形成「粘滞到坏端点」直到 TTL 到期。

---

### A6. 熔断器与舱壁租约

**业务语义**：每个端点维度一个三态熔断器 + 并发信号量；流式请求的租约延迟到 body 消费完成才结算。

**状态**：`closed` → `open`（错误率达阈值）→ `half-open`（OpenTimeout 后）→ `closed`（足够探测成功）/ `open`（探测失败）。（`enum/breaker_state.go`）

**转移**

| 当前 | 事件 | 守卫 | 下一 | 证据 |
|---|---|---|---|---|
| closed | 上报失败 | 窗口请求数 ≥ MinRequests 且错误率 ≥ 阈值 | open | `circuit_breaker.go:132-140` |
| open | `Allow()` | `now - openSince ≥ OpenTimeout` | half-open（放行 1 个探测） | `circuit_breaker.go:89-97` |
| half-open | 探测成功 | 累计 ≥ HalfOpenMaxRequests | closed（清窗口） | `circuit_breaker.go:118-124` |
| half-open | 探测失败 | — | open | `circuit_breaker.go:125-128` |
| 任意 | 租约 `Fail()` 后 `Done()` | — | 强制上报失败 | `transport/guard.go:85-102` |

**隐式点**：熔断器本身有显式状态字段，但「流式请求的成败在 body Close 时才确定」这一层没有状态类型，靠 `GuardLease.failed` 原子布尔 + `leaseBoundBody` 的 Read/Close 顺序表达（`transport/guard.go:157-200`）。OAuth 端点用稳定 key `oauth:{endpointID}` 保证 token 轮换不重置熔断（`usecase/common.go:241-242`）。

**风险**

- 半开期只允许 `HalfOpenMaxRequests` 个并发探测，探测请求若被业务层提前取消，可能既不成功也不失败，熔断器停在半开（取决于调用方是否 `Done`）。
- `breaker()` 在 key 数量超上限时整体重建 map（`resilience/guard.go:127-131`），会丢失全部熔断窗口。

---

### A7. 会话快照续写合并（keeper / redundant）

**业务语义**：每次请求把完整历史落库并插一条 Session 快照；插入后按「归属 API Key + 首条消息 ID」分组，把新快照前滚到既有 keeper 并软删新行，保证会话 ID/评分/分享关系跨轮稳定。

**状态（单行 Session）**：`新插入` → `keeper`（承接续写）/ `redundant`（软删）/ `独立分支`（保留）。

**转移**

| 当前 | 事件 | 守卫 | 下一 | 证据 |
|---|---|---|---|---|
| 新插入 | 组内存在「是本快照前缀」的会话 | 最长优先、等长取 ID 最小 | 新行软删，keeper 前滚 | `session_dedup.go:328-341` |
| 新插入 | 组内无前缀关系（首轮/分叉） | — | 保留为新 keeper | `session_dedup.go:343-347` |
| 既有行 | 新快照是它的前缀（重试旧轮次） | — | 只并 tool_ids，不覆盖快照 | `session_dedup.go:375-395` |
| 新插入 | 组内已有更长快照 | — | 判为 redundant 软删 | `session_dedup.go:135-150` |
| 合并失败 | — | — | 仅日志，残留由下次同组插入自愈 | `store_pool.go:130-137` |

**隐式点**：这是典型的「算法即状态机」——状态不是字段，而是 `MessageIDs` 前缀关系 + 软删标记推导出来的；keeper 的选择、等长去重、跨租户防护（`FindSessionTarget` 内二次校验 `APIKeyID`）都是隐式不变量（`session_dedup.go:184-201`）。

**风险**

- 合并是插入事务提交后的 best-effort 短事务（`store_pool.go:127`），失败时用户会短期看到重复会话；自愈依赖「下次同组插入」。
- `messages` 表按 checksum **全局**去重，跨租户复用同一 message 行；一旦分组键漏掉 `api_key_id`，会把内容写进他人会话（代码已有多处注释警告，说明这是高危点）。
- 详情缓存只失效 `touchedIDs`（keeper 与被并过 tool_ids 的容器，`session_dedup.go:309`、`store_pool.go:149-152`）；被软删的冗余行不在其中，若其详情曾被缓存，最长 1h 内仍可读到已删会话。

---

### A8. 会话生命周期（active → soft-deleted → purged）

**业务语义**：用户删除是软删；终态清理 cron 删除「在 tool_call 处中断」的会话；软删清理 cron 硬删不再被引用 message/tool 及其图片。

**状态**

| 状态 | 判定 | 证据 |
|---|---|---|
| `active` | `deleted_at = 0` | `model/base.go:15` |
| `soft-deleted` | `deleted_at != 0` | `session/command/delete_session.go:59` |
| `terminal-stuck` | 最近 24h、`updated_at` 早于 30min、末条消息为 assistant + tool_calls | `cron/session_terminal_cleanup.go:128-148` |
| `purged` | message/tool 被硬删 | `cron/soft_delete_purge.go:121-181` |

**转移**

| 当前 | 事件 | 守卫 | 下一 | 证据 |
|---|---|---|---|---|
| active | 用户/admin 删除 | 归属校验 | soft-deleted | `delete_session.go:42-70` |
| active | 终态清理 cron | 24h 窗口 + 30min 空闲 + 末条 tool_call | soft-deleted | `session_terminal_cleanup.go:124-159` |
| soft-deleted | 软删清理 cron | 不再被任何 active 会话引用 | message/tool 硬删 + 图片清理 | `soft_delete_purge.go:137-181` |

**隐式点**：「终态」不是存下来的状态，而是每次扫描时用「末条消息角色/工具调用 + 空闲阈值」重新推导的；`CronTerminalCleanupIdleThreshold` 的注释明确说明不加阈值会误删进行中的 agent 工具循环（`constant/session.go:19-23`）。

**风险**

- 30min 空闲阈值对长时工具循环（例如沙箱内跑 40 分钟）会误判为「中断」，把进行中的会话删掉；阈值是全局常量，不能按客户端调整。
- 软删清理的差集计算基于「当前所有 active 会话」，并发插入期间新会话引用的 message 可能被误判为孤儿（窗口很窄，但存在）。

---

### A9. Blob 上传状态

**业务语义**：消息中的图片按 sha256 去重，压缩后上传对象存储；失败进入退避重试。

**状态**：`pending` / `uploaded` / `failed` / `unprocessable`（`enum/blob_status.go`）。

**转移**

| 当前 | 事件 | 守卫 | 下一 | 证据 |
|---|---|---|---|---|
| — | 新图提交 | 队列可入 + 内存预算内 | 异步处理 | `blobstore/uploader.go:80-102` |
| — | 队列满 / 预算超限 / 排空中 | — | 直接写 `pending`（原图） | `uploader.go:90-97, 138-148` |
| 处理中 | 压缩成功 + PUT 成功 | — | `uploaded` | `uploader.go:162-170` |
| 处理中 | PUT 失败 | — | `pending` + `next_retry_at` + attempts=1 | `uploader.go:167-170` |
| 处理中 | 无法压缩到 2MB | — | `unprocessable` | `uploader.go:163-164` |
| pending/failed | 重试 cron | 到达 `next_retry_at` | 成功 `uploaded` / 继续退避 / attempts≥10 `failed` | `blobstore/retry.go:95-123` |

**隐式点**：`pending` 同时表示「排队中」「上传失败待重试」「排空时未压缩」，三种语义共用一个值；`attempts` 与 `next_retry_at` 是状态机的隐藏维度，查询侧不看它们就无法区分「刚提交」与「重试 9 次」。

**风险**

- `Submit` 的「已存在则跳过」（`inflight` map，`uploader.go:81-84`）与 DB 唯一键是两层去重，进程重启后 inflight 清空，可能对同一 sha 并发处理两次（DB 侧 `InsertIgnoreConflict` 兜底）。
- `unprocessable` 的图仍保留在 `blobs.data`，只有软删清理才会删除；大图长期占库。

---

### A10. Cron 执行生命周期（含分布式锁与启用态）

**业务语义**：定时任务在多副本下靠 Redis 锁互斥；管理端可启停、改 spec、手动触发；每次执行写审计。

**状态**

- 调度态：`enabled` / `disabled`（DB `cron_jobs.enabled`）。
- 运行态：`skipped`（DB 禁用）/ `not-acquired`（锁被占，不产生记录）/ `success` / `failed` / `panic`（`constant/cron.go:34-38`）。
- 触发来源：`scheduled` / `manual`。

**转移**

| 当前 | 事件 | 守卫 | 下一 | 记录 | 证据 |
|---|---|---|---|---|---|
| scheduled 触发 | 读 DB | `enabled=false` | skipped | 写 `skipped` 审计 | `cron/lock_runner.go:150-157` |
| scheduled 触发 | 抢锁 | 失败 | 结束 | **不写记录** | `lock_runner.go:159-164` |
| 抢锁成功 | 执行 | 返回 error | failed | 写 failed | `lock_runner.go:166-168` |
| 抢锁成功 | 执行 | 正常 | success | 写 success | `lock_runner.go:170` |
| manual 触发 | 抢锁 | 失败 | 结束 | **不写记录** | `lock_runner.go:204-214` |
| manual 触发 | 抢锁成功 | 后台执行 | success/failed/panic | 写记录 | `lock_runner.go:216-237` |
| 任意 | panic | — | panic | 写 panic | `lock_runner.go:266-272` |
| enabled | admin 关闭 | — | disabled | 停实例 + pub/sub 广播 | `cron/manager.go:223-230`；`cronmgmt/command/update_cron_job.go:69-71` |
| disabled | admin 开启 | — | enabled | 建实例 + 广播 | `manager.go:252-258`；`update_cron_job.go:74-81` |

**隐式点**

- 「DB 启用态」与「进程内调度器是否存在」是两份状态，靠 Redis pub/sub 收敛（`manager.go:113-156`）；消息丢失时没有补偿轮询，只有进程重启才会重新对齐。
- 锁续期失败只停止续期、不中断任务（`lock_runner.go:96-127`），因此存在「锁已过期、任务仍在跑、另一副本又拿到锁」的重叠窗口——业务任务被假定幂等。
- `manual` 触发是 fire-and-forget：接口返回 true 只代表「拿到锁并起了 goroutine」，前端无法得知执行结果（`cronmgmt/command/trigger_cron_job.go:32-36`）。

**风险**

- 手动触发后立刻刷新审计列表，可能看不到本次记录（异步写入）。
- 锁丢失后的重叠执行对非幂等任务（如 blob 重试、trigger 计数同步）是真实风险，当前靠「任务幂等」的设计假设兜底。

---

### A11. 触发词热更新与三动作

**业务语义**：AC 自动机在内存中匹配触发词；增删改后经 pub/sub + 版本号 + 低频兜底重建；命中后按 `deny > capture > omit` 处置。

**状态**

- 匹配器代际：`in-sync` / `stale`（版本号落后）/ `rebuild-failed`（保留旧自动机）。
- 单次请求处置：`no-hit` / `deny`（短路拦截）/ `capture`（保存历史并固定回复）/ `omit`（照常转发、跳过存储）。

**转移**

| 当前 | 事件 | 守卫 | 下一 | 证据 |
|---|---|---|---|---|
| in-sync | 收到 pub/sub | — | rebuild | `trigger/service.go:93-99` |
| stale | 版本轮询发现 `version != lastSeen` | rebuild 成功才推进游标 | in-sync | `service.go:132-142` |
| rebuild-failed | 低频兜底（≥5min） | — | 再试 | `service.go:124-128` |
| no-hit | 文本匹配 | — | 转发 | `usecase/openai.go:182-215` |
| hit(deny) | — | `DenyIDs` 非空 | 200 + 协议原生 content_filter | `usecase/openai.go:185-198` |
| hit(capture) | 词落在最后一条 user 且无 tool_call_id | capture 生效 | 保存历史、不请求上游、固定回复 | `usecase/trigger_capture.go:59-85` |
| hit(capture) | 词只在历史/system | — | 不短路，照常转发 | 同上 |
| hit(omit) | — | `OmitIDs` 非空 | 转发 + `CtxKeySkipStore` | `usecase/openai.go:212-214` |

**隐式点**：「capture 是否生效」不是状态字段，而是「命中位置 + 是否存在历史」两个条件的组合；omit 与 capture 可同时生效（设计上不短路），这个组合态只写在 `CONTEXT.md` 的一段话里，代码用两个独立 `if` 表达。

**风险**

- 版本游标 `lastSeenVersion` 只在 rebuild 成功后推进（`service.go:137-141`），若 DB 持续失败，会每 2s 重试一次 rebuild（有日志放大风险）。
- `Rebuild` 持写锁做全量 DB 查询（`service.go:52-69`），高频变更时 `Check`（读锁）会被阻塞。

---

### A12. API Key 鉴权缓存（吊销生效窗口）

**业务语义**：鉴权结果缓存 30s（+0~6s 抖动），命中零 DB 查询；吊销写 DB 即删行，但缓存不做主动失效。

**状态**：`active`（DB 有行且 `deleted_at=0`）→ `revoked`（软删，`deleted_at` 置位）；缓存视角另有 `cached-active`，在 TTL 内仍按 active 放行。

**转移**

| 当前 | 事件 | 守卫 | 下一 | 证据 |
|---|---|---|---|---|
| active | 用户/admin 吊销 | 归属校验 | 软删（`deleted_at` 置位） | `apikey/command/revoke_api_key.go:46-78`；`dao/base.go:80-85` |
| cached-active | TTL 到期或抖动到期 | — | 回源 → 401 | `middleware/apikey.go:60-90`；`constant/apikey.go:18-23` |

**隐式点**：吊销是「写路径」，缓存失效依赖 TTL；代码注释明确这是有意的取舍（命令层不持有 Redis），因此「吊销后仍可用 30s」是设计态而非 bug。但状态机的真实终态是「DB 软删 + 缓存过期」，两者时间点不同，任何安全审计都需要知道这个窗口。

**风险**

- 用户在 30s 内用已吊销 Key 发起的请求仍会被计费/计审计。
- 删除用户时级联删除 API Key（`user_repository.go:329-331`）同样受这个窗口影响。

---

### A13. 请求排空与优雅关闭

**业务语义**：收到 SIGTERM 后停止 cron、进入 draining（新请求 503）、两阶段排空在途请求、再依次关 HTTP/日志/DB/Redis。

**状态**：`running` → `draining` →（soft 到点）`cancel-broadcast` →（hard 到点）`force-stop`。

**转移**

| 当前 | 事件 | 守卫 | 下一 | 证据 |
|---|---|---|---|---|
| running | `Track()` | 非 draining | 计数 +1 | `common/inflight/tracker.go:28-38` |
| running | `Drain()` | — | draining；新请求 503 | `tracker.go:53-55`；`middleware/inflight.go:25-35` |
| draining | 全部请求完成 | soft 窗口内 | 结束 | `tracker.go:62-65` |
| draining | soft 超时 | — | 广播 cancel，派生 ctx 取消 | `tracker.go:66-69, 95-105` |
| cancel-broadcast | hard 超时 | — | 返回 false，由 HTTP shutdown 兜底 | `tracker.go:74-77` |
| draining | OnStop 顺序 | — | cron → inflight → pool → metrics → HTTP → logger → trigger → DB → Redis | `bootstrap/lifecycle.go:40-143` |

**隐式点**：关闭顺序由「fx OnStop 反序执行」+ 7 个 `Append` 的注册顺序隐式决定（`lifecycle.go:41-44` 的注释是唯一规格）；`CancelOnDrain` 必须由调用方显式 `cancel`，否则 goroutine 泄漏（`tracker.go:82-95` 的长注释记录了生产事故）。

**风险**

- 关闭顺序一旦有人插入新 hook，反序语义会被破坏，且没有测试守护。
- `CancelOnDrain` 的 cancel 责任在 body Close 路径上，漏调即泄漏；当前依赖 handler/adapter 的 defer（`api/util/adapter.go:126-138`）。

---

### A14. 审计状态分类与「终局提交」

**业务语义**：每次模型调用落一条审计，状态码四态（200 / 具体 HTTP / -1 连接错误 / 0 未知）；触发词 capture 的标注写在 `error_message` 但不计入失败。

**状态**：`success` / `error(http)` / `error(connection)` / `error(unknown)` / `success-with-annotation`。

**转移**：由 `CallStatus.IsError()` 单点判定（`domain/modelcall/vo/call_status.go:65-83`）；跨端点 fallback 期间的失败经 `CtxKeyFailureAuditDeferral` 暂存，只有终局失败提交（`usecase/common.go:245-277`）。

**隐式点**：审计的「写没写」是异步池的副作用（`pool/store_pool.go:269-297`），队列满时 `SubmitModelCallAuditTask` 返回 error 但调用方 `_ =` 忽略（`usecase/recorder.go:165`），审计静默丢失；没有 `dropped` 计数或补偿。

**风险**

- 审计丢失不会影响响应，也不会告警；对账（计费/配额）会缺口。
- 中间失败被丢弃（设计如此），若上游已计费，平台成本与审计不一致。

---

### A15. 分享链接（Redis TTL + 反向索引）

**状态**：`active`（share key 存在）→ `expired`（key 过期，但 zset 记录保留到 retention）→ `deleted`（显式删除）。

**证据**：`infrastructure/cache/share.go:66-124`（创建：占位 + pipeline）、`:352-371`（列表时用「key 是否存在 + 过期时间」重建状态）、`:198-232`（删除）。

**隐式点**：同一个「已过期」在列表接口里还分「已过期但仍在 retention 内（展示）」与「过期太久（隐藏）」，由 `shareRecordToItem` 的两个时间比较隐式决定（`:352-371`）。会话删除不会清理其分享链接，`getSharedSession` 的鉴权依赖后续查询。

---

### A16. Token 限流桶（peek / deduct）

**状态**：桶余额 `>0` / `≤0`（拒绝）；请求后扣减可 transient 为负（`token_rate.go:87`）。

**转移**：请求前 peek（`token_rate.go:148+`）、响应后 `TokenUsageReporter.Report` 扣减（`:103-133`），按时间线性回填。隐式点：失败请求不扣减；流式中断若没走到 usage 上报，本次用量不扣，桶会偏高。

---

## 2. Web 端（`web/src/`）

> 前端没有全局状态机库；状态分散在 `useState` / `useRef` / `localStorage` / URL / 请求时序里。共 10 个主要状态机，多数与「同一事实的多个写者」和「竞态守卫覆盖不均」相关。

### W1. 登录态与 Token 生命周期

**状态**：`S0 bootstrapping` → `S1 anonymous` / `S2 refreshing`（只有 refresh token）/ `S5 authenticated`；交互分支 `S3 authorizing` → `S4 exchanging`；隐藏错误态 `S6 expired`（storage 已清、React 内存态仍是已登录）。

**转移**

| 事件 | 源 → 目标 | 副作用 | 证据 |
|---|---|---|---|
| 挂载读 storage | S0 → S1/S2/S5 | 三分支 | `lib/auth-context.tsx:74-104` |
| `/user` 成功 | S0/S2 → S5 | `setUser`；demo 再拉模块 | `auth-context.tsx:45-67` |
| 启动刷新失败 | S2 → S1 | 清两个 key + `setAccessToken(null)` | `auth-context.tsx:95-100` |
| 点 provider | S1 → S3 | 整页跳授权；4s 后清 `pending` | `app/login/page.tsx:34-43` |
| 回调 code+state | S3 → S4 | 换 token → `handleCallback` → 跳 dashboard | `login/page.tsx:45-87`；`app/callback/page.tsx:18-58` |
| demo 登录 | S1 → S4 | 同一 `handleCallback` | `login/page.tsx:106-127` |
| 任意 401 | S5 → S2 → S5/S6 | `tryRefreshToken` 单飞；失败清凭据 | `lib/api-client.ts:152-195, 203-227` |
| 登出 | S5 → S1 | 仅清本地 | `auth-context.tsx:133-139` |

**隐式点**：没有 `AuthState` 判别联合；真值分散在 `user / accessToken / isLoading` + 两处 localStorage。凭据有 **3 个写者**（`AuthProvider.logout`、`api-client.tryRefreshToken`、`api-client.clearAuthAndPromptLogin`），`getHeaders` 每次直接读 storage（`api-client.ts:137-150`）。

**风险**

- `S6` 在 React 里不可见：`clearAuthAndPromptLogin` 只清 storage + toast，不动 `AuthProvider.user`（`api-client.ts:229-243`）；`PermissionGuard` 仍渲染已登录外壳（`components/permission-guard.tsx:62-64`），随后每个请求都 401，toast 会连发。
- 两份 access token：401 刷新只写 localStorage（`api-client.ts:176`），context 里的 token 永久过期；登录页跳转判据用的正是它（`login/page.tsx:100-104`）。
- 刷新无超时/Abort，一次挂起的 refresh 会阻塞所有等待同一 Promise 的 401。
- `/login` 与 `/callback` 各实现一遍回调换码逻辑，改协议会漂移。

### W2. 身份权限与路由准入

**状态**：身份 `anonymous / pending / demo / user / admin`；准入结果 `allow / deny-static / pending-screen / redirect-login / render-null`；demo 子态 `module open/locked`。

**判定表**

| 身份 | 有 module 且开放 | 有 module 未开放 | adminOnly | 裸 Guard |
|---|---|---|---|---|
| anonymous | 跳 /login | 跳 /login | 跳 /login | 跳 /login |
| pending | pending 屏 | pending 屏 | pending 屏 | pending 屏 |
| demo | allow | deny | deny | deny |
| user | allow | allow | deny | allow |
| admin | allow | allow | allow | allow |

证据：`components/permission-guard.tsx:52-103`；导航裁剪 `lib/nav.ts:164-178`；布局壳 `app/(dashboard)/layout.tsx:404`。

**隐式点**：身份枚举是显式的，但「身份 × 路由 → 结果」被四套独立推导重复实现：`PermissionGuard`、`resolveNavGroups`、命令面板二次过滤（`components/nav-command-palette.tsx:57-73`）、各页面手填的 `module=`。迁移动作以 if-chain 写死在用户页（`app/(dashboard)/users/page.tsx:179-211`）。

**风险**

- **准入 fail-open**：`app/(dashboard)/harness/page.tsx` 全文件没有 `PermissionGuard`（导航却标了 `adminOnly`，`nav.ts:94`），普通用户直接输 URL 可进入并发起 admin 请求；`profile/page.tsx` 同样无 `module` 守卫，demo 可打开可编辑表单（写接口由后端拒绝）。
- demo 授权是登录时一次性快照（`auth-context.tsx:53-62`），管理员关闭模块后长开标签页仍可点可进，直到刷新。
- 用户页 `acting` 只记动作类型不记目标 id（`users/page.tsx:115`），并发操作会互相清态；提升用户无二次确认。

### W3. Playground 单轮流式生成

**状态**：`T0 no-model` → `T1 ready` → `T2 sending`（占位已插、无首 token）→ `T3 streaming` → `T4 done` / `T5 interrupted`（AbortError）/ `T6 error`。会话作用域另有 `activeId`。

**转移**

| 事件 | 源 → 目标 | 副作用 | 证据 |
|---|---|---|---|
| 发送/编辑重发/重跑 | T1 → T2 | 插占位、`setSending(true)`、建 controller | `app/(dashboard)/playground/page.tsx:163-168, 216-219, 256-269` |
| 首个 delta/reasoning | T2 → T3 | 首 token 计时 + rAF 合批 | `playground/page.tsx:222-232` |
| 流结束 | T2/T3 → T4 | `finalize({})` 写 meta + persist | `playground/page.tsx:185-214, 235` |
| Stop | T2/T3 → T5 | `abortRef.current?.abort()` | `playground/page.tsx:418`；`components/playground/composer.tsx:310-319` |
| 401 | → T4（静默） | `finalize({})` | `playground/page.tsx:241-242` |
| 其它错误 | → T6 | 错误卡 + Retry | `playground/page.tsx:243-250` |

**隐式点**：`sending` 是全局布尔，不携带会话/轮次标识；`placeholder.id`、`pending` 文本、`abortRef` 都是闭包变量；`finalize` 幂等性未定义。

**风险**

- **删除当前会话后输入静默失效**：`handleDeleteSession` 不更新 `activeId`（`page.tsx:349-359`），渲染用 `sessions[0]` 兜底（`:362-363`），但 `handleSend` 按 `activeId` 找不到就 `return`（`:259-260`），Composer 仍可点。
- **`sending` 跨会话串扰**：流进行中可切会话（侧栏未禁用），新会话 Composer 变成 Stop、最后一条 assistant 被标成流式中；点 Stop 会 abort 旧会话的流。
- **错误卡 Retry 绕过 `sending` 闸门**：`handleRetry = handleRerun`（`:313`），`handleRerun` 没有 `sending` 守卫（`:288-299`），而错误卡按钮不受 `sending` 限制（`components/playground/chat-turn.tsx:241-255`）→ 可启动第二条流并覆盖 `abortRef`。
- persist 写在 `setSessions(prev => { persist(next); ... })` 的 updater 内（`:196-211, 326, 342, 354`），React 丢弃渲染时写盘已发生。
- 无超时：上游不返回时 `sending` 永远为真，只能手动 Stop。

### W4. 会话详情 / 分享页装载与消息分页

**状态**：`D0 loading` → `D1 ready` → 消息子态 `D4 loading` / `D5 partial` / `D6 exhausted`；错误 `D2 not-found` / `D3 error`（分享页）/ `D7 silent-failed`（只 warn）。

**转移**：`components/session-detail/session-detail-client.tsx:73-87, 135-157, 167-182`；`hooks/use-infinite-list.ts:42-91`；`app/share/page.tsx:107-148, 216-218`。

**隐式点**：`loading/metadata/score/error` 与 hook 内的 `items/total/offset/loaded/hasMore` 分属两个 hook；「切换 id」不是显式事件，没有 reset 分支。

**风险**

- **同一路由 query 变化不重挂载**：`app/(dashboard)/sessions/detail/page.tsx:9-11` 把 `?id=` 作为 prop 传入，组件无 `key`；`useInfiniteList` 提供了 `reset()`（`use-infinite-list.ts:73-83`）但详情页从未调用（`session-detail-client.tsx:143-157`）。于是切换会话后 metadata 会重拉、消息列表仍是旧会话，`offset/total` 也是旧值，下一次 `loadMore` 从旧偏移请求新会话，直接跳过前 N 条。
- 元数据失败既不 `setMetadata(null)` 也不置错误标志（`:73-87`），页面仍显示上一会话内容。
- 分享页 `setError(null)` 从未调用（`share/page.tsx:107-148`），而 `if (error) return` 在渲染顺序最前（`:216-218`），从失效分享回退到有效分享仍停在错误页。
- 消息分页失败只 `console.warn`（`use-infinite-list.ts:61-63`），无重试入口。

### W5. 列表查询-分页-刷新-选择

**状态**：`L0 first-load` / `L1 ready` / `L2 empty-data` / `L3 empty-filtered` / `L4 silent-refresh` / `L5 error-stale`；选择 `none / partial / all`；行内操作 `idle / busy(rowId)`。

**转移**：`hooks/use-paginated-list.ts:63-113`（请求序号 + 持久化页码 + 静默刷新）；`app/(dashboard)/sessions/page.tsx:197-201, 246-280`。

**隐式点**：数据层只有 `items/pageInfo/loading`；空态语义、错误态、选择集合都由调用方用 `tokens.length` / `selected.size` 另算；页码是持久化外部状态，不与 `total` 对账。

**风险**

- **持久化页码不对账**：`page` 从 localStorage 恢复、服务端 `pageInfo.page` 原样回写（`use-paginated-list.ts:64-68, 95-99`），没有 `page ≤ ceil(total/pageSize)` 钳制；数据变少后刷新会停在空页、分页条仍显示旧页码。
- **批量选择跨页残留**：选择只在查询条件变化时清（`sessions/page.tsx:198`），翻页不清；「全选」按当前页判断（`:246-252`），删除会带上不可见的上一页选中项。
- 失败态不可见：多数页面不传 `errorTitle`，列表保留旧数据且没有「已过期」标记（`use-paginated-list.ts:90-93`）。
- `useFilterBar` 的 `loadingKey` 是单槽（`components/filter-bar/use-filter-bar.ts:93-109`），两个异步 facet 并发时先完成者会关掉后者的 loading；选项失败返回 `[]`，与「没有候选值」不可区分。
- 行内评分 `scoring` 是单个 `number|null`（`sessions/page.tsx:95`），A 行评分中点击 B 行会被静默丢弃。

### W6. 业务页表单与确认弹窗

**状态**：`F0 closed` → `F1 editing` → `F2 submitting` → `F3 success` / `F4 failure`；确认框 `C0 open` → `C1 submitting` → `C2 closed`；apikeys 另有 `K0 secret-revealed`。

**转移**：`app/(dashboard)/apikeys/page.tsx:94-114, 135-139`；`app/(dashboard)/upstream/page.tsx:340-394, 555-613`；`hooks/use-delete-confirm.ts:40-83`；`components/delete-confirm-dialog.tsx:56-58`。

**隐式点**：每页各自维护 `open/saving/creating/editing/target`；「确认框打开」与「请求进行中」是两个独立 state，未建模互斥。

**风险**

- **确认框 loading 态不可达**：`AlertDialogAction` 本身是 `AlertDialogPrimitive.Close`（`components/ui/alert-dialog.tsx:97-104`）。点击时 `onConfirm` 置 loading，同一次点击又触发 `onOpenChange(false)`，父级受控 `open` 立刻变 false（`use-delete-confirm.ts:60-64`）→ 弹层在请求完成前消失，`loadingLabel` / `disabled` 形同虚设，失败也没有「留在弹窗重试」的路径。
- **apikeys 回车双提交**：输入框 `onKeyDown` 直接调 `handleCreate`（`apikeys/page.tsx:193-195`），而 `handleCreate` 没有 `creating` 守卫（`:94-96`），连按回车会创建多把 Key，只有最后一次明文可见。
- 一次性明文可被 Esc/遮罩/关闭按钮一次误点销毁（`:135-139, 148-153`），无「我已保存」确认。
- cron 表达式保存的 `if (rsp.error)` 分支不可达（`api-client` 对业务错误直接 throw），异常穿透为未处理 rejection（`app/(dashboard)/cron/page.tsx:97-109`；`components/cron/schedule-editor.tsx:190-199`）。
- 模型规格导入 `fetchSpec` 在 `await` 后无条件 `setForm`（`components/upstream/model-dialog.tsx:164-186`），等待期间改 `upstreamModel` 会把旧模型规格写进新模型。

### W7. Demo 配置与白名单

**状态**：配置卡 `C0 loading` / `C1 clean` / `C2 dirty` / `C3 saving` / `C4 saved` / `C5 load-failed`（卡片消失）；demo 侧 `A0 loading` / `A1 loaded` / `A2 fail-closed([])`；白名单成员 `in/out/pending`。

**证据**：`components/demo/demo-config-card.tsx:22-73`；`hooks/use-demo-whitelist.ts:26-88`；`auth-context.tsx:53-62`。

**隐式点**：`config` 一个对象同时承载「已保存值」和「编辑中草稿」，没有 dirty/savedAt；`demoModules` 是登录时快照，无 TTL/版本号；白名单在列表页与详情页各实例化一份、各拉全量。

**风险**：配置加载失败整卡 `return null`（`:71-73`）；保存失败后本地仍是「已改」状态，界面无法区分生效与未保存；管理员关闭模块后 demo 长开标签页仍显示可进入，直到刷新。

### W8. Upstream 双视图与 OAuth 接入

**状态**：视图 `grouped/flat`；两套独立列表状态（`loading/ready/empty/error`）；OAuth 子态 `O0 none → O1 authorizing → O2 url-ready → O3 code-submitting → O4 success / O5 error`。

**证据**：`app/(dashboard)/upstream/page.tsx:141-145, 404-472, 633-693, 903-910`；`hooks/use-model-list.ts:25-75`。

**隐式点**：两个视图各有一套分页/排序/筛选与竞态守卫（`loadSeqRef` vs `useRequestSeq`）；OAuth 用 `oauthMode + oauthBusy + oauthAuthorizeURL + oauthForm + reauthTarget` 五个 state 描述，没有 flow 标识。

**风险**

- **OAuth 不按 provider 隔离**：`oauthBusy` 是单布尔（`:106`），切换 provider 后旧请求返回仍会写 `state`（`:413`），provider 与 state 可能错配；会同时打开两个授权标签页。
- 关闭弹窗只 `resetOAuthFlow`（`:905-909`），进行中的授权请求返回后仍会把已打开的标签页导航到授权页（孤儿标签页）。
- 同一模型的 enabled 开关在分组视图走乐观更新 + 回滚（`:633-651`）、平铺视图走「等响应 → 整表 reload」（`:685-693`），且无单飞；连点会发送相同的 `!enabled`。
- `refreshAll` 两条链路各自失败只 toast，一侧成功即视为完成。

### W9. 自动刷新 / 轮询

**状态**：`off` / `armed-visible` / `paused-hidden` / `fetching` / `failed`；Dashboard 用 `scope + refreshToken`，Monitor 用 `range + sinceRef`。

**证据**：`hooks/use-auto-refresh.ts:25-53`；`hooks/use-runtime-metrics.ts:42-82`；`hooks/use-dashboard-query.ts:40-67`；`components/dashboard/panel.tsx:40-56`。

**风险**

- **Monitor 轮询无序号守卫**（`use-runtime-metrics.ts:42-70`）：切 range 时旧请求若后到，会用旧 `cutoff`/旧点集合并进新窗口并推进 `sinceRef`；Dashboard 有 seq（`use-dashboard-query.ts:46-51`），Monitor 没有。
- `refreshNow` 不检查在途请求（`use-auto-refresh.ts:35-38`），定时器、手动、回前台三条路径可叠加；慢网时在途请求随档位累积。
- 有数据时刷新失败不可见：`Panel` 只在 `error && empty` 时给重试（`panel.tsx:40-56`）。

### W10. 主题 / 语言 / 动效档位

**状态**：每个偏好 `applied → transitioning → applied`，异常回退 `fallback`；动效档位另有单向 `still` 闩锁。

**证据**：`lib/theme.tsx:121-147`；`lib/motion/theme-transition.ts:44-83`；`lib/i18n.tsx:60-90`；`lib/motion/tier-store.ts:12-42`。

**隐式点**：同一事实 3–5 份副本（DOM 属性 + localStorage + React state + ref + 非 React 的每次探测）；转场是模块级单例，与 React 状态没有所有权关系。

**风险**：主题目标值由 React state 推导（双击时第二次仍读旧值）；系统「减弱动态」把档位写成 `still` 后永不恢复，`matchMedia` 监听器注册后不移除；无跨标签页 `storage` 同步；`Accept-Language` 只发送 `zh|en`，`ja` 被静默丢弃（`api-client.ts:144-147`）。

---

## 3. CLI 端（`cmd/client/` + `internal/client/`）

> 状态寄居在文件系统、并列 bool、零值结构体、goroutine 生命周期与 `error` 返回值里。

### C1. `aris init` 三步向导

**状态**：`S0 终端解析` → `S1 主机` → `S2 健康检查` → `S4 Key` → `S5 Key 校验` → `S7 保存` → `S8 完成`；旁路 `S3/S6 重试确认`；终态 `E 错误` / `A 用户中止`。

**转移**：`internal/client/setup/wizard.go:51-102`（主流程）、`:138-179`（两个重试循环）、`:225-243`（Confirm 默认 Continue）。

| 当前 | 事件 | 守卫 | 下一 | 证据 |
|---|---|---|---|---|
| S0 | stdin 是 TTY，或 `/dev/tty` 打开成功 | — | S1 | `wizard.go:51-55, 181-191` |
| S0 | `/dev/tty` 打不开 | — | E | 同上 |
| S1 | `NormalizeHost` 通过 | 非空 + `http(s)://` 前缀 | S2 | `:58-71, 193-205` |
| S1 | 空串 / 缺 scheme | — | E | 同上 |
| S2 | `GET /health` 2xx | 5s 超时 | S4 | `:138-149` |
| S2 | 非 2xx / 网络错误 | — | S3 | `:150-157` |
| S3 | Confirm 选择 Continue（默认 true） | — | S2（**同一 host/client**） | `:233-243, 138-158` |
| S3 | Confirm 选择 Cancel / Ctrl-C | — | E / A | 同上 |
| S4 | env 或输入非空 | 存量可回填 | S5 | `:58-71, 108-136` |
| S5 | `GET .../client/check` 2xx | — | S7 | `:161-169` |
| S5 | 非 2xx | — | S6 | `:170-177` |
| S6 | Continue（默认） | — | S5（**同一 key**） | `:233-243` |
| S7 | temp + chmod 0600 + write + sync + rename 成功 | — | S8 | `config/config.go:95-128` |
| S7 | 任一步失败 | — | E | 同上 |

**隐式点**：步骤号只是渲染参数（`printStep`），没有 `stage` 字段；「已初始化」只存在于文件系统；中止用 `huh.ErrUserAborted` 表示。

**风险**：host/key 在循环外定型，重试只重试同一个值（`wizard.go:138-158, 161-178`），打错 host 或 env key 无效时可无限循环，没有「返回修改」转移；`NormalizeHost` 只校验前缀（`:193-205`），`https://` 这类串也能通过；损坏配置被 `_` 丢弃后直接覆盖（`:58`）。

### C2. 客户端配置读写（新路径 + legacy）

**状态**：`MissingBoth` / `LegacyOnly` / `ValidNew` / `CorruptNew` / `UnreadableNew`。

**转移**：`internal/client/config/config.go:62-81`（Load：新优先、仅在新缺失时回退旧）、`:84-128`（Save：mkdir 0700 + temp 0600 + Sync + rename）。

**风险**：新文件损坏时**不会**回退 legacy（`:66-79`）；Save 无 `.bak`；legacy 只读不迁移，双份长期并存；无锁，两个进程 last-writer-wins。

### C3. 目标配置文件生命周期（5 个 harness）

**状态**：`Absent` / `UnmanagedValid` / `ManagedValid` / `LegacyProvider` / `Corrupt` / `Unreadable`。

**转移**：`internal/client/model/atomic.go:53-85`（统一 backup + temp + 0600 + rename）；各 target 在 `targets.go`（OpenCode/Pi）、`text_targets.go`（Claude/Codex）、`dsh_target.go`（DSH）里各自解析与合并。

**风险**：`backupAndWrite` 只 `Write→Close→Rename`，**没有 `tmp.Sync()`**（`atomic.go:74-81`）；`.bak` 每次覆盖且非原子（`:54-58`），第二次导出后原始配置不可恢复；空文件被当作「不存在」（`:96-98`）；读错误导致备份被静默跳过（`:54`）。

### C4. 导出模型条目生命周期（同名刷新 / 自加保留 / 下架残留）

**状态**：`Absent` / `Exported` / `ExportedStale` / `UserAdded` / `Refreshed`。

**各目标语义**

| 目标 | 同名条目 | 用户自加条目 | 本次未选中的旧导出 |
|---|---|---|---|
| OpenCode | 整条替换 | 保留 | **保留（永久残留）** |
| Pi | 整条替换 | 保留 | **保留（永久残留）** |
| Claude Code | 只写 3 个 tier env | 其它 env 保留 | **旧 tier 变量残留** |
| Codex | 整个 provider 段重建 | provider 内自定义键丢失 | N/A |
| DSH | 保留手写字段，只刷新受管字段 | **删除** | **删除** |

**证据**：`targets.go:193-223`（OpenCode 整条替换、用户自加保留、未选中残留）、`:301-321`（Pi）、`text_targets.go:46-56`（Claude 旧 tier 变量不清）、`text_targets.go:194-217`（Codex 整段重建）、`dsh_target.go:194-232`（DSH 删除未选中）。

**风险**：5 个目标对「删除/覆盖」有 5 种语义，同一操作在不同 harness 上结果相反；OpenCode/Pi 的未选中条目永久残留；条目内用户自定义字段（如 `reasoningEfforts`）在 OpenCode/Pi 上被整条覆盖丢失，DSH 反而保留。

### C5. DSH 双文件一致性

**状态**：`PairNone` / `CredsOnly` / `Consistent` / `Inconsistent`。

**转移**：`dsh_target.go:51-58`（先写凭证）、`:79`（再写 patch）、`:284-334`（凭证 upsert）。

| 当前 | 事件 | 守卫 | 下一 | 证据 |
|---|---|---|---|---|
| 任意 | profile 目录不存在 | — | 拒绝（不变） | `dsh_target.go:52-54` |
| PairNone | 凭证写成功、patch 写失败 | — | CredsOnly | `:56, 79` |
| CredsOnly | 下一次导出、patch 可解析 | — | Consistent | `:59-79` |
| Consistent | 重复导出 | — | Consistent（幂等） | `test/unit/client/model/dsh_test.go:293-322` |
| Consistent | 凭证写成功、patch 写失败 | — | Inconsistent | `:56, 79` |
| Inconsistent | 重跑成功 | — | Consistent | 同上 |

**风险**：两次独立写、无事务；patch 写失败时凭证已被替换，系统停在 `Inconsistent`，`status` 只 stat patch 看不出来；`.credentials.yaml.bak` 会多留一份明文 key。

### C6. Codex TOML 扫描器

**状态**：`Root` / `OtherTable` / `ProfilesTable` / `Memories` / `Provider` / `Multiline`。

**证据**：`text_targets.go:101-150`（正则 + 多行跟踪）、`:243-287`（主循环）、`:194-217`（重排输出）。

**风险**：不是 TOML 解析器；`dropRootKeys` 对任何非 profiles 表都为 true，`[tui]` 等表里的 `model` 键会被删；`memories` 段为空时整段不输出；文件每次被重排。

### C7. `aris update` 自更新

**状态**：`S0 解析源` → `S1 探测 tag` → `S2 版本比较` → `S3 下载/校验/解包` → `S4 替换`；终态 `up-to-date` / `done` / 4 类错误。

**证据**：`internal/client/update/update.go:41-95`（主流程）、`:272-315`（临时文件 0700 + Sync + rename + 目录 fsync）、`:99-125`（重定向取 tag）。

| 当前 | 事件 | 守卫 | 下一 | 证据 |
|---|---|---|---|---|
| S0 | `resolveBaseURL` | https 或 loopback http | S1 | `update.go:318-363` |
| S0 | 非 https 且非 loopback | — | E_SOURCE | 同上 |
| S1 | HEAD 302 且路径含 `/download/<tag>/` | 平台矩阵 | S2 | `:99-125, 128-140` |
| S1 | 无跳转 / 超时 / tag 不可解析 | — | E_PROBE | 同上 |
| S2 | `IsUpToDate` | current ≥ latest | T_UP | `version.go:45-52` |
| S2 | 需要更新（含 current 不可解析） | — | S3 | `update.go:72-95` |
| S3 | 下载 + sha256 + 解包成功 | 归档 64MB 限制 | S4 | `:165-269` |
| S3 | 任一步失败 | — | E_DOWNLOAD | 同上 |
| S4 | CreateTemp + Chmod 0700 + Write + Sync + Close + Rename | — | T_DONE | `:272-315` |
| S4 | 任一步失败 | — | E_REPLACE | 同上 |

**风险**：rename 后旧二进制立即消失，无回滚；目标文件恒为 0700，原属主/权限被改写；目录不可写要到下载完才失败；探测请求用覆盖后的 `CheckRedirect`，绕过了逐跳 `validateBaseURL`；解包用 `io.ReadAll(io.LimitReader(...))` 超限时**静默截断**；镜像必须 302，否则永远失败。

### C8. 使用中更新检查

**状态**：`Disabled`（门禁不过）/ `Skipped`（update/version）/ `Probing` / `Notified` / `Silent` / `Timeout`。

**证据**：`cmd/client/root.go:33-63`；`internal/client/update/check.go:27-67`。

**风险**：无论探测是否完成，`finishCheck` 最坏等 400ms；提示可能出现在错误信息之后；探测复用 `Latest`，同样依赖 302。

### C9. 交互中断（spinner）

**状态**：`Sync`（非 TTY）/ `Async`（TTY）/ `Done` / `Aborted`。

**证据**：`internal/client/ui/async.go:16-58`。

**风险**：中止只改变 UI 返回值，**不取消 action goroutine**（`:22-24`），Ctrl-C 后写盘可能仍在进行，留下 `*.tmp-*`；action panic 时没有 `recover`，bubbletea 永远等不到完成消息。

### C10. `aris status` 聚合

**状态**：配置维度 2 态 × 服务端 2 态 × 鉴权 2 态 × providers 3 态。

**证据**：`internal/client/status/checks.go:32-83`；`render.go:22-78`。

**风险**：健康检查失败仍 exit 0（`status.go:42-53`），脚本无法据此分支；「host 有、key 空」被算作已初始化；`os.Stat` 错误等价于缺失。

---

## 4. 横向模式

### 4.1 状态载体的五个层次

| 载体 | 例子 | 失效方式 | 风险特征 |
|---|---|---|---|
| DB 列 | `permission`、`blob.status`、`oauth_credential.status`、`deleted_at` | 写路径显式更新 | 无转移守卫；改库绕过缓存失效 |
| Redis | API Key 鉴权缓存、JWT 用户缓存、OAuth access/flow、亲和、分享、限流桶 | TTL | 存在「DB 已变、缓存未变」窗口 |
| 进程内 | 熔断器、触发词自动机、cron 调度器、inflight tracker | 进程重启 | 多副本不一致，靠 pub/sub 收敛 |
| 文件系统 | `~/.aris/config.json`、5 个 harness 配置、DSH 双文件 | 原子替换 / 崩溃残留 | 三套原子写语义不一致 |
| 前端 storage / DOM | `access_token` / `refresh_token`、页码与筛选 token、`data-theme` / `data-motion-tier` / `lang` | 手动清理 / 无失效 | 与 React 内存态不同步，可能出现「半登录」「权限快照过期」 |
| 请求 context | `CtxKeySkipStore`、`CtxKeyFailureAuditDeferral`、`CtxKeyTokenUsageReporter` | 请求结束 | 隐式控制流，调用方漏读即行为改变 |

### 4.2 五类系统性缺陷

1. **守卫在调用方，不在聚合根**：`User.ChangePermission`、`Endpoint.SetVisibility`、`Model.Update` 的前置条件都写在 command/handler；聚合根只保证字段合法（`SetVisibility` 校验枚举），不保证「从哪个状态来」合法。新增入口时容易绕过。
2. **影子状态**：同一事实存在两份（DB + cache、DB + 调度器、patch + credentials、前端 user + localStorage token），同步靠「写路径记得删缓存 / pub-sub 广播 / 下次请求自愈」，没有一处统一。
3. **中间态没有落点**：会话合并、审计写入、blob 重试、cron 执行、CLI 写盘都没有可查询的「进行中/失败待重试」状态（blob 是唯一例外），失败只能从日志推断，也无法做补偿。
4. **布尔爆炸替代状态枚举（前端最突出）**：`isLoading / saving / creating / scoring / sending / oauthBusy / deleting …` 各自为政，且多数不携带归属对象（哪个会话、哪一行、哪个 provider），这是「跨会话串扰」「并发操作互相清态」的共同根因。
5. **竞态守卫逐处手写、覆盖不均**：同一仓库有 `useRequestSeq`、`loadSeqRef`、`seqRef`、`generationRef` 四种写法；Playground 轮次、Monitor 轮询、会话详情换 id 都没有守卫，而 Dashboard、模型列表有。

### 4.3 文档与代码漂移

| 文档 | 声明 | 代码现状 |
|---|---|---|
| `CONTEXT.md` DemoConfig | `sampleModulus` 取模抽样 | 不存在；改由 `demo_sessions` 白名单 |
| `CONTEXT.md` Demo 模块 | `dashboard/sessions/audit/models/trigger/endpoints/monitor/cron/cron_audit` | 已合并为 `upstream`（`enum/demo_module.go`、`web/src/lib/demo-modules.ts`） |
| `CONTEXT.md` API Key 鉴权 | 「吊销/轮换最迟 30s 生效」 | 与代码一致（`constant/apikey.go:18`），但轮换接口不存在 |

---

## 5. 收敛建议（方向，不含实现）

1. **把「合法转移表」变成数据**：给权限、blob、OAuth 凭据、cron 各写一张 `from → event → to` 表（可以是 Go 的 `map[state]map[event]state`），聚合根/命令层共用；非法转移返回统一错误。收益最大的是权限与 OAuth 凭据。
2. **区分「状态」与「派生视图」**：`demo 可登录`、`会话终态`、`端点可用` 都是派生状态，显式命名成函数并集中测试，不要让中间件/前端各算一遍。
3. **影子状态收敛**：API Key 吊销加主动失效（或在响应头/审计里标注缓存窗口）；admin 提权补一个 CLI/管理命令并统一失效缓存；cron 的 DB 启用态与调度器状态加周期性对齐（不只依赖 pub/sub）。
4. **前端把准入与异步动作收敛成「一张表 + 一个 hook」**：路由准入由 `RouteAccess`（与 `NAV_GROUPS` 同源）统一裁决，删掉各页面手填的 `module`；异步动作统一为 `idle | running(key) | success | error` 且强制携带目标 key，替换 `saving/creating/scoring/sending/oauthBusy`。
5. **前端的幂等与重置**：OAuth 回调、登录页回调、删除确认统一用 `useRef` 做一次性守卫；详情页以 `sessionId` 作 `key` 强制重挂载（或显式 `reset()` 消息列表）；`AuthProvider` 的 `fetchUser` 失败时同时清 token，避免半登录态。
6. **CLI 原子写统一**：抽 `internal/client/atomicfile`，把 `Sync`、父目录 fsync、`.bak` 保留代数、临时文件清理变成显式选项；`init` 的重试改为「显式 Retry 状态 + 允许返回修改输入」。
7. **补状态转移测试**：权限表、blob 状态、cron 执行、会话合并各补一组「每条边一个 case」的测试；Web 补 OAuth 回调重复执行、切换 `?id=`、确认框提交期的回归；CLI 补中断残留与并发写。

---

## 附录：明确判定为「不是状态机」的部分

| 位置 | 判定 |
|---|---|
| `internal/domain/agentharness/rule.go:31` `MatchRule` | 无状态最长前缀查找，不是 FSM |
| `internal/application/llmproxy/usecase/compat_route.go` | 请求协议 × 端点能力的决策表，无状态 |
| `internal/domain/llmproxy/vo/pricing.go:154` `Pricing.Match` | 第一命中 + 末档回落，纯函数 |
| `web/src/lib/nav.ts` 导航裁剪 | 纯派生，无转移 |
| `internal/client/api/client.go` | 无状态 HTTP 客户端 |
| `internal/application/metrics/query/runtime_metrics.go:114` 实例 liveness | 由快照窗口推导，可视为监控派生态而非业务 FSM |
