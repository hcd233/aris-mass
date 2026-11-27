# Chat Completions → Responses 协议转换设计（Codex 订阅可用化）

> 日期：2026-11-26
> 状态：待评审
> 关联：`docs/superpowers/specs/2026-10-18-oauth-subscription-pooling-design.md` §4.2（原把 Chat→Response 列为非目标，本设计补上该路径）

## 1. 背景与目标

### 1.1 为什么做

用户的 agent harness 大多只支持 OpenAI Chat Completions。当前 Codex 订阅端点（`oauth_openai`）只开了 Responses 能力，`SelectCompatRoute` 又没有「转成 Responses」这条路线，导致 Chat 客户端命中 Codex 模型时候选被过滤、返回 model not found，订阅额度无法被这些 harness 利用。

### 1.2 目标

- 新增 **Chat Completions 客户端 → Responses 上游** 的完整转换路径：普通文本、多轮历史、工具调用（含工具结果回传的多轮循环）、流式与非流式、reasoning 内容、usage 入账。
- 让 `oauth_openai`（Codex 订阅）端点对 `/api/openai/v1/chat/completions` 可用；非流式 Chat 请求由网关聚合上游 SSE 后返回完整 ChatCompletion。
- 最大化复用现有转换组件，不重写第二套协议模型。

### 1.3 非目标

- Anthropic `/messages` → Responses 上游（Claude Code 客户端用 Codex 订阅）。本次不做，架构上预留（见 §8）。
- Responses 客户端 → Responses 上游的非流式聚合（`CreateResponse` 对 Codex 仍要求 `stream=true`，维持现状）。
- Chat 的 `n > 1` 多候选、logit_bias、audio、prediction、web_search_options 等 Responses 无对应语义的参数。
- Codex 上游的 `previous_response_id` 续链（Chat 协议无此概念，历史全量转换）。
- 任何前端页面、管理 API、数据库 schema 变更。

### 1.4 反目标

- 为 Chat→Response 单独造一套 DTO / SSE 解析器 / 聚合器。
- 只在 `oauth_openai` 上硬编码特判，导致普通 api_key 的 Responses-only 端点仍不可用。
- 非流式路径把上游 SSE 原文当 JSON 返回（旧 spec §4.2 明确禁止的缺陷形态）。

## 2. 现有架构盘点（复用依据）

### 2.1 路由层

`usecase/compat_route.go` 的 `SelectCompatRoute(requestAPI, ep)` 按端点能力选路，当前枚举：

| CompatRoute | 含义 |
|---|---|
| `CompatRouteNative` | 客户端协议 = 上游协议 |
| `CompatRouteViaOpenAIChat` | 上游讲 Chat Completions |
| `CompatRouteViaAnthropicMessage` | 上游讲 Anthropic Messages |

Chat 入口现状：`native → viaAnthropicMessage → unsupported`。Responses-only 端点落到 `unsupported`，在 resolver 候选过滤阶段被剔除。

### 2.2 转换层（`application/llmproxy/converter`）

以 `OpenAIChatCompletionReq/Chunk` 为**中间基座**的双向转换：

| 文件 | 方向 | 可复用点 |
|---|---|---|
| `response.go` | Response→Chat（`FromResponseRequest`）、Chat→Response 响应（`ToResponseResponse`） | `chatContentToResponseContent`（Chat content→Responses content parts）、`chatMessageToResponseOutputs`（assistant 消息→reasoning/function_call/message items）、`buildTextOutputItem`、`resolveToolCallOutputType`、`splitNamespacedName`、`buildCustomToolCallItem`、`BuildToolTypeMap` |
| `response_stream.go` | Chat chunk→Responses SSE（`WriteResponseDeltaFromChatChunk` / `FinalizeResponseFromChatCompletion`） | 本设计的**逆方向**，事件枚举与 DTO 可复用，状态机需新写 |
| `openai.go` / `openai_sse.go` | Anthropic↔Chat | `openAIChatViaAnthropicStream` 是本设计的结构模板（converter + aggregator + sink） |
| `anthropic_response.go` | Response→Anthropic | 无直接复用 |

关键事实：`ResponseInputItem` 同时建模 Responses 的 input 与 output item；`dto.ResponseStreamTerminalEvent` / `ResponseStreamOutputItemDoneEvent` 已覆盖终态与 item.done 事件；`enum.ResponseStreamEvent*` 已定义所需事件名（缺 reasoning summary 增量，需补常量）。

### 2.3 传输与聚合

- `OpenAIProxyPort` 已有 `OpenCreateResponseStream` + `ReadCreateResponseStream(onEvent(event, data))`，**新增路径不需要任何 transport/DI 改动**。
- Codex 特化 `transformCodexOAuthBody` 在 transport 层强制 `store=false`、`stream=true` 并清理上游不接受字段——所以转换器**不需要针对 Codex 特判**，按标准 Responses 语义产出即可。
- `proxyutil.ChatCompletionStreamAggregator`（Add/Completion/Count）负责把 Chat chunk 聚成完整 completion，流式存储与非流式响应都靠它。
- `proxyutil.NormalizeOpenAIStreamToolCalls` 保证流式 tool_calls 每个 chunk 都带 id/index（harness 兼容性的关键细节）。
- `proxyutil.IsResponseAPITerminalEvent` / `FillResponseTerminalOutput` 处理「终态 output 为空、靠 output_item.done 累积」的上游行为。

### 2.4 存储与审计

- `storeOpenAIChatFromCompletion(ctx, req, completion, err, modelID)`：Chat 入口统一的会话存储，与协议来源无关，直接复用。
- `recordModelCall` + `responseTokenUsage{rsp}`：以上游 Responses usage 入账（`input_tokens` 含 cached，净输入=总量−命中，符合 TokenAccounting 口径）。
- `openAIChatRouteUpstreamProtocol(compatRoute)`：补新分支，触发词 deny/capture 审计的 `upstream_protocol` 记为 `openai-response`。

## 3. 路由设计

`enum.CompatRoute` 新增 `CompatRouteViaOpenAIResponse`。Chat 入口优先级：

```
ProxyAPIOpenAIChat:
  SupportOpenAIChatCompletion  → Native            （不变）
  SupportAnthropicMessage      → ViaAnthropicMessage（不变，避免改变存量双能力端点行为）
  SupportOpenAIResponse        → ViaOpenAIResponse  （新增兜底）
```

`ProxyAPIOpenAIResponse` / `ProxyAPIAnthropicMessage` / `ProxyAPIOpenAIDecision` 的选路不变。

效果：Codex 订阅端点（仅 Responses）对 Chat 客户端变为可选候选；与其他候选之间仍走既有 `runWithFallback`（流建立前失败可切换端点）。

创建端点时，仓储必须在同一事务中保留显式传入的 Chat 能力 `false`，不能让 GORM 的 `default:true` 将 Responses-only 端点误写为同时支持 Chat。已有错误的 OAuth 端点需要修正能力标记后才能按上述规则选路。

## 4. 请求转换（Chat → Responses）

新文件 `converter/response_from_chat.go`，方法挂在既有 `ResponseProtocolConverter` 上：

```go
func (c *ResponseProtocolConverter) ToResponseRequest(req *dto.OpenAIChatCompletionReq) (*dto.OpenAICreateResponseReq, error)
```

### 4.1 标量映射

| Chat | Responses | 说明 |
|---|---|---|
| `model` | `model` | 之后由 `MarshalOpenAIResponseBodyForModel` 覆写为上游真实模型名 |
| `max_completion_tokens`（缺省回退 `max_tokens`） | `max_output_tokens` | Codex 会清掉，标准端点保留 |
| `temperature` / `top_p` / `top_logprobs` | 同名 | |
| `reasoning_effort` | `reasoning.effort` | |
| `verbosity` | `text.verbosity` | |
| `response_format` | `text.format` | `json_schema` 的 name/schema/strict/description 平移（`responseTextFormatToChat` 的逆映射） |
| `metadata` / `parallel_tool_calls` / `prompt_cache_key` / `prompt_cache_retention` / `safety_identifier` / `store` / `user` / `service_tier` | 同名 | |
| `stream` | `stream` | **恒置 true**（见 §5：统一上游流式） |
| `n>1`、`stop`、`logit_bias`、`logprobs`、`audio`、`modalities`、`prediction`、`seed`、`thinking`、`web_search_options`、frequency/presence_penalty | 不映射 | Responses 无对应语义；`n>1` 返回转换错误（不支持多候选），其余静默丢弃 |

### 4.2 消息序列 → instructions + input

- `system` / `developer` 消息：抽出文本按原序以 `\n\n` 连接为 `instructions`；无此类消息时 `instructions` 置为空字符串指针（保证字段存在，不引入来源不明的默认文案；Codex 若拒绝空值再按旧 spec §4.3 的预案处理）。
- `user` 消息：`{type:"message", role:"user", content:[...]}`，content 复用 `chatContentToResponseContent(content, input_text)`，文本/图片 parts 均支持。
- `assistant` 消息：复用 `chatMessageToResponseOutputs` 的工具调用映射，但**丢弃 reasoning**（与 Response→Chat 方向丢弃 reasoning item 对称：Codex `store=false` 下没有可回放的加密 reasoning，回传摘要文本只会污染 prompt）。产出 message item（`output_text`，复用 `buildTextOutputItem`）+ function_call/custom_tool_call item。
  - 实现上把 `chatMessageToResponseOutputs` 抽一个 `includeReasoning bool` 的内部共用函数，原函数签名不变（避免影响既有 Response 输出路径）。
- `tool` 消息：`{type:"function_call_output", call_id, output}`，`tool_call_id` 原样保留（harness 多轮工具循环的闭环关键）；纯文本直接放 `output` 字符串，多模态 parts 走 `output` 的 content 数组形态。

### 4.3 工具定义与 tool_choice

- `tools[]`：Chat `function` → Responses `function`（name/description/parameters/strict 平移）；Chat `custom` → Responses `custom`。
- 转换后用既有 `BuildToolTypeMap` 生成 name→原始类型映射，供 §4.2 assistant 历史把 custom 工具调用还原为 `custom_tool_call`（复用 `buildCustomToolCallItem` 的 `{"content":...}` 拆包逻辑）。namespace 概念 Chat 侧不存在，`namespaceMap` 为空。
- `tool_choice`：字符串模式（none/auto/required）原样；命名函数 → `{type:"function", name}`（`responseToolChoiceToChat` 的逆映射）。

## 5. 转发与响应转换

### 5.1 统一上游流式

`forwardChatViaResponse` **无论客户端 stream 与否，都走 `OpenCreateResponseStream`**：

- Codex 强制 stream=true，统一路径避免为非流式再写一套 Responses JSON→Chat 转换；
- 普通 Responses-only 端点同样适用（SSE 是 Responses 的标准能力）；
- 客户端非流式 = 同一套事件转换 + 聚合，最后返回 JSON，杜绝「SSE 原文当 JSON」的缺陷形态。

### 5.2 Responses SSE → Chat chunk（新文件 `converter/response_chat_stream.go`）

转换器持有状态：`chunkID`（`chatcmpl-` 转换后缀，同既有转换路径）、`toolCallIDs`（index→id）、`itemID→toolCallIndex`、是否已见工具调用、累积 output items、终态 response。核心方法：

```go
func (c *ResponseToChatStreamConverter) HandleEvent(event string, data []byte) ([]*dto.OpenAIChatCompletionChunk, error)
func (c *ResponseToChatStreamConverter) FinalResponse() *dto.OpenAICreateResponseRsp
```

| 上游事件 | 产出 Chat chunk |
|---|---|
| `response.output_text.delta` | `delta.content` |
| `response.reasoning_text.delta`、`response.reasoning_summary_text.delta`（新增枚举常量） | `delta.reasoning_content` |
| `response.output_item.added`（function_call/custom_tool_call） | 分配 tool call index；`delta.tool_calls=[{index,id:call_id,type,function.name}]`，同时补一条首帧 role=assistant |
| `response.function_call_arguments.delta` | `delta.tool_calls=[{index,function.arguments}]`（按 item_id 找 index） |
| `response.custom_tool_call_input.delta` | `delta.tool_calls=[{index,custom.input}]` |
| `response.output_item.done` | 累积完整 item（终态 output 为空时兜底，复用 `FillResponseTerminalOutput` 思路）；message item 内 refusal part → `delta.refusal` |
| `response.completed` | 末帧 `finish_reason`（有工具调用=`tool_calls`，否则=`stop`）+ `usage`（input/output/total + cached_tokens + reasoning_tokens 明细） |
| `response.incomplete` | 末帧 `finish_reason`：`max_output_tokens`→`length`，`content_filter`→`content_filter`，usage 同上 |
| `response.failed` | 不产出正常末帧，记录终态供 usecase 走 `WriteUpstreamSSEError` |
| 其余事件（created/in_progress/content_part.* 等） | 忽略 |

所有产出 chunk 过 `NormalizeOpenAIStreamToolCalls`，保证 id/index 规范；首条内容/工具 chunk 携带一次 `role=assistant`（chunk `object=chat.completion.chunk`，ID 用 `chatcmpl-converted` 系列，同既有转换路径）；首帧标记 `IsResponseAPIDeltaEvent` 用于 first-token 延迟口径（与 native Response 一致）。

### 5.3 usecase 装配（新文件 `usecase/openai_chat_response.go`）

结构完全对齐 `openAIChatViaAnthropicStream`：

- **流式**：`Read` 内 `ReadCreateResponseStream` 逐事件 → `HandleEvent` → 每个 chunk `agg.Add` + `sink.WriteEvent`；结束写 `[DONE]`；`storeOpenAIChatFromCompletion(agg.Completion())`；`recordModelCall{upstreamProtocol: openai-response, apiProtocol: openai-chat-completion, usage: responseTokenUsage{finalRsp}}`。
- **非流式**：同一转换器与 aggregator，sink 替换为纯聚合；结束后归一化 `completion.Object = chat.completion`（aggregator 继承的是 chunk 的 `chat.completion.chunk`，必须覆写）、`completion.Model = 客户端别名`，JSON 返回；存储与审计同上。
- 上游打开失败：`auditFailureWithProviders(openai-response → openai-chat-completion)` + `ProxyErrorFromUpstream`，交给 `runWithFallback` 判定是否换端点。
- `dispatchChat` 增加 `case enum.CompatRouteViaOpenAIResponse`。

## 6. 错误与边界语义

| 场景 | 行为 |
|---|---|
| 端点只支持 Responses | 正常走新路径（此前 model not found） |
| 客户端 `n>1` | 转换错误，不进入上游（与现有转换失败错误语义一致） |
| 上游 `response.failed` | SSE 错误帧 + 审计失败；流已交付不切换端点（既有约束） |
| 上游流中断 | 沿用 `ReadCreateResponseStream` 的 `UpstreamConnectionError` 与熔断上报 |
| 客户端中途断开 | 既有 drain/ctx 取消链路，无需新增处理 |
| 触发词 deny/capture/omit | 不变；仅审计的 upstream_protocol 新增映射 |
| 会话存储 | 与 Chat native 一致（聚合后的 completion），`CtxKeySkipStore` 语义不变 |

## 7. 改动清单

| 文件 | 改动 |
|---|---|
| `internal/common/enum/llmproxy_compat.go` | 新增 `CompatRouteViaOpenAIResponse` |
| `internal/common/enum/openai_response.go` | 新增 reasoning summary 增量事件常量 |
| `internal/application/llmproxy/usecase/compat_route.go` | Chat 入口新增兜底分支 |
| `internal/application/llmproxy/usecase/openai.go` | `dispatchChat` 新增 case |
| `internal/application/llmproxy/usecase/trigger_capture.go` | `openAIChatRouteUpstreamProtocol` 新增映射 |
| `internal/application/llmproxy/converter/response.go` | `chatMessageToResponseOutputs` 抽 `includeReasoning` 内部共用函数（签名不变） |
| `internal/application/llmproxy/converter/response_from_chat.go` | 新增：§4 请求转换 |
| `internal/application/llmproxy/converter/response_chat_stream.go` | 新增：§5.2 流式转换 |
| `internal/application/llmproxy/usecase/openai_chat_response.go` | 新增：§5.3 流式/非流式装配 |
| `CONTEXT.md` | Cross-Protocol Conversion 7→8 条路径；EndpointAuthType 中 oauth_openai「仅接受 Responses」改为「Responses 原生 + Chat 跨协议转换」 |
| `test/unit/converter/`、`test/unit/llmproxy_usecase/` | 单元测试 |
| `test/e2e/chat_to_response/` | 新增 E2E |

无 transport、DI、DTO、huma 路由、前端、DB 改动。

## 8. 预留：Messages → Responses

未来若要让 Claude Code 走 Codex 订阅，路径为 `Anthropic req → Chat req`（`OpenAIProtocolConverter.FromAnthropicRequest` 已有）→ 本设计 `ToResponseRequest`，响应方向逆走即可。届时只需在 `SelectCompatRoute` 的 Anthropic 分支加兜底与装配函数，转换层无需重做。本期不实现。

## 9. 测试与验收

### 9.1 单元测试

- 路由：Responses-only 端点 Chat 入口选中 `ViaOpenAIResponse`；chat+response 双能力端点仍 `Native`；anthropic+response 端点仍 `ViaAnthropicMessage`（优先级回归）。
- 请求转换：system/developer→instructions 合并；user 多模态 parts；assistant 带 tool_calls → function_call item 且 reasoning 被丢弃；tool 消息→function_call_output（call_id 保留）；custom 工具定义与历史 custom_tool_call 拆包；response_format/reasoning_effort/max_tokens 映射；`n>1` 报错。
- 流式转换：文本+reasoning 增量；两个并行 function_call 的 added/arguments 增量（index 分配与 id 规范化）；终帧 finish_reason=tool_calls + usage 明细；incomplete→length/content_filter；failed→错误路径；终态 output 为空时 item.done 兜底。
- 聚合：流式 chunk 序列经 aggregator 得到的 completion 与非流式返回一致（复用 `ConcatChatCompletionChunks` 语义断言）。

### 9.2 E2E（`test/e2e/chat_to_response/`）

真实 `RegisterAPIRouter` + sqlite + miniredis + httptest 模拟 Responses 上游（SSE），api_key 端点仅开 Responses 能力（Codex 的 body 特化已有 `test/unit/oauth_upstream` 覆盖，不重复搭 OAuth）：

1. 非流式 Chat + tools：断言上游收到 Responses 形态（instructions/input/tools、`stream=true`），客户端拿到完整 ChatCompletion（content + tool_calls + usage），会话与审计落库且 upstream_protocol=openai-response。
2. 流式 Chat：断言 chunk 序列含 role/content/tool_calls/finish_reason 与 `[DONE]`。
3. 多轮工具循环：第二轮请求带 assistant tool_calls + tool 消息，断言上游 input 含 function_call 与 function_call_output 且 call_id 对应。
4. 回归：Chat 能力端点仍走 native（上游收到 Chat 形态）。

### 9.3 验证命令

`go test ./test/unit/converter/... ./test/unit/llmproxy_usecase/... ./test/e2e/chat_to_response/...` + 仓库 lint（`go run ./cmd/lint conv ./...`、`golangci-lint`）。

## 10. 实施顺序

1. 枚举与路由分支 + 单测（路由层先行，验证候选不再被过滤）。
2. 请求转换 + 单测。
3. 流式转换器 + 单测。
4. usecase 流式/非流式装配 + 触发词审计映射。
5. E2E 四类用例。
6. CONTEXT.md 更新 + 全量测试/lint + ponytail-review。
