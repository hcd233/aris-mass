# Chat → Responses 协议转换实施计划

> **For agentic workers:** 本会话内逐任务实施（不使用子 Agent）。步骤用 checkbox 跟踪。

**Goal:** 让只支持 Chat Completions 的客户端（agent harness）可以使用 Responses-only 上游（重点是 Codex 订阅端点），含工具调用多轮循环、流式与非流式。

**Architecture:** 在既有 `SelectCompatRoute` 增加 `CompatRouteViaOpenAIResponse` 兜底；converter 层新增 Chat→Responses 请求转换与 Responses SSE→Chat chunk 流式转换，复用现有 DTO/聚合器/工具映射；usecase 统一走上游 SSE，非流式聚合为完整 ChatCompletion。

**Tech Stack:** Go（现有 `dto`、`sonic`、`samber/lo`）、既有 `OpenAIProxyPort`、`ChatCompletionStreamAggregator`。

**Spec:** `docs/superpowers/specs/2026-11-26-chat-to-response-conversion-design.md`

## Global Constraints

- 不改动 transport、DI、DTO、huma 路由、前端、DB；不引入第三方依赖。
- 非流式路径不得把上游 SSE 原文当 JSON 返回；非流式响应 `object` 必须归一化为 `chat.completion`。
- Chat 入口选路优先级：`Native → ViaAnthropicMessage → ViaOpenAIResponse`，其余入口选路不变。
- 转换层不针对 `oauth_openai` 特判（Codex 特化仍在 transport）。
- 遵守仓库 lint：`ierr` 错误、无 `errors.New`、业务包无本地 const 块（字面量入 `constant`/`enum`）、测试放 `test/` 外部包。
- 不执行 git commit/push（等用户明确要求）。

## Review Focus

- 并行多个 function_call：index 分配必须稳定，arguments 增量不能串到别的调用 → Task 3 流式测试。
- 上游终态 `output` 为空：需用 output_item.done 累积补齐，否则非流式丢内容 → Task 3/5。
- 非流式 `object` 字段继承 chunk 值 → Task 4 归一化断言。
- 工具结果回传：`tool_call_id` 必须原样变成 `function_call_output.call_id`，否则第二轮循环断裂 → Task 2/5。
- 存量双能力端点（chat+response、anthropic+response）选路不能被新分支改变 → Task 1 回归测试。

---

### Task 1: 路由枚举与 Chat 入口选路

**Files:**
- Modify: `internal/common/enum/llmproxy_compat.go`
- Modify: `internal/application/llmproxy/usecase/compat_route.go`
- Test: `test/unit/llmproxy_usecase/compat_route_response_test.go`（新建）

**Interfaces:**
- Produces: `enum.CompatRouteViaOpenAIResponse`（`CompatRoute` 新常量，追加在现有值之后）；`SelectCompatRoute(enum.ProxyAPIOpenAIChat, responseOnlyEp) == enum.CompatRouteViaOpenAIResponse`。

- [ ] Step 1: 写失败测试：response-only 端点 Chat 入口返回新路由；chat+response 端点仍 `Native`；anthropic+response 端点仍 `ViaAnthropicMessage`；response-only 端点的 Response 入口仍 `Native`、Anthropic 入口仍 `Unsupported`。
- [ ] Step 2: `go test ./test/unit/llmproxy_usecase/ -run TestSelectCompatRoute -v` 确认新用例 FAIL。
- [ ] Step 3: 实现枚举常量与 Chat 分支兜底。
- [ ] Step 4: 重跑确认 PASS，且既有 `TestSelectCompatRoute_*` 全绿。

### Task 2: Chat → Responses 请求转换

**Files:**
- Create: `internal/application/llmproxy/converter/response_from_chat.go`
- Modify: `internal/application/llmproxy/converter/response.go`（`chatMessageToResponseOutputs` 抽 `includeReasoning` 内部共用函数，公开签名不变）
- Test: `test/unit/converter/response_from_chat_test.go`（新建）

**Interfaces:**
- Produces: `func (c *ResponseProtocolConverter) ToResponseRequest(req *dto.OpenAIChatCompletionReq) (*dto.OpenAICreateResponseReq, error)`。
- Consumes: 既有 `chatContentToResponseContent`、`buildTextOutputItem`、`BuildToolTypeMap`、`buildCustomToolCallItem`、Task 1 无依赖。

- [ ] Step 1: 写失败测试，覆盖：
  - system+developer 两条消息合并为 `instructions`（`\n\n` 连接）；无 system 时 `instructions` 为非 nil 空串。
  - user 文本+图片 parts → `input` message item（`input_text`/`input_image`）。
  - assistant 带 `reasoning_content`+tool_calls：输出无 reasoning item、有 function_call item（`call_id`/`name`/`arguments` 保留）。
  - tool 消息 → `function_call_output`，`call_id` 与上游 tool_call 对应。
  - function/custom 工具定义映射；custom 历史调用拆包为 `custom_tool_call.input`。
  - `response_format(json_schema)`→`text.format`；`reasoning_effort`→`reasoning.effort`；`max_completion_tokens`→`max_output_tokens`（缺省回退 `max_tokens`）；`stream` 恒为 true。
  - `n=2` 返回 error。
- [ ] Step 2: `go test ./test/unit/converter/ -run TestToResponseRequest -v` 确认 FAIL（方法不存在）。
- [ ] Step 3: 实现转换与 `includeReasoning` 重构。
- [ ] Step 4: 重跑 PASS；`go test ./test/unit/converter/...` 既有用例全绿（重构无行为变化）。

### Task 3: Responses SSE → Chat chunk 流式转换

**Files:**
- Modify: `internal/common/enum/openai_response.go`（补 reasoning summary 增量事件常量）
- Create: `internal/application/llmproxy/converter/response_chat_stream.go`
- Test: `test/unit/converter/response_chat_stream_test.go`（新建）

**Interfaces:**
- Produces:
  - `type ResponseToChatStreamConverter`（字段含 exposedModel、chunkID、toolCallIDs、itemID→index、累积 output、终态 response）
  - `func NewResponseToChatStreamConverter(exposedModel string) *ResponseToChatStreamConverter`
  - `func (c *ResponseToChatStreamConverter) HandleEvent(event string, data []byte) ([]*dto.OpenAIChatCompletionChunk, error)`
  - `func (c *ResponseToChatStreamConverter) FinalResponse() *dto.OpenAICreateResponseRsp`
- Consumes: `proxyutil.NormalizeOpenAIStreamToolCalls`、`proxyutil.IsResponseAPITerminalEvent`、`dto.ResponseStreamTerminalEvent`、`dto.ResponseStreamOutputItemDoneEvent`。

- [ ] Step 1: 写失败测试，按 spec §5.2 事件表逐类断言：
  - `output_text.delta`→content；reasoning_text/reasoning_summary delta→reasoning_content；首条 chunk 带 `role=assistant`。
  - 两个并行 function_call：added 分配 index 0/1 并带 id+call_id+name；arguments.delta 按 item_id 落到正确 index；每个 chunk 经 id 规范化。
  - `response.completed`：末帧 finish_reason=`tool_calls`（无工具时 `stop`）+ usage（含 cached/reasoning 明细）；`FinalResponse()` 返回终态对象。
  - `response.incomplete`：`max_output_tokens`→`length`、`content_filter`→`content_filter`。
  - `response.failed`：不产生正常末帧，`FinalResponse()` 记录 failed。
  - 终态 `output` 为空时用 output_item.done 累积补齐 `FinalResponse().Output`。
  - created/in_progress/content_part.* 等事件返回空。
- [ ] Step 2: `go test ./test/unit/converter/ -run TestResponseToChatStream -v` 确认 FAIL。
- [ ] Step 3: 实现转换器与事件常量。
- [ ] Step 4: 重跑 PASS。

### Task 4: usecase 装配（流式 + 非流式 + 审计映射）

**Files:**
- Create: `internal/application/llmproxy/usecase/openai_chat_response.go`
- Modify: `internal/application/llmproxy/usecase/openai.go`（`dispatchChat` 新 case）
- Modify: `internal/application/llmproxy/usecase/trigger_capture.go`（`openAIChatRouteUpstreamProtocol` 新分支）
- Test: `test/unit/llmproxy_usecase/openai_chat_response_test.go`（新建）

**Interfaces:**
- Consumes: Task 1 路由、Task 2 `ToResponseRequest`、Task 3 转换器；既有 `OpenAIProxyPort.OpenCreateResponseStream/ReadCreateResponseStream`、`storeOpenAIChatFromCompletion`、`recordModelCall`、`responseTokenUsage`。
- Produces: `func (u *openAIUseCase) forwardChatViaResponse(ctx context.Context, req *dto.OpenAIChatCompletionRequest, m *aggregate.Model, ep *aggregate.Endpoint) (port.Result, error)`。

- [ ] Step 1: 写失败测试（mock `OpenAIProxyPort`，与既有 forward 测试同模式）：
  - response-only 端点非流式 Chat：上游 body 为 Responses 形态且 `stream=true`；返回 JSON `object=chat.completion`、model 为客户端别名、content/tool_calls/usage 正确。
  - 流式：写出 chunk 序列含 role/content/tool_calls/finish_reason，末尾 `[DONE]`。
  - 审计 `upstream_protocol=openai-response`、`api_protocol=openai-chat-completion`。
- [ ] Step 2: `go test ./test/unit/llmproxy_usecase/ -run TestForwardChatViaResponse -v` 确认 FAIL。
- [ ] Step 3: 实现装配（结构对齐 `openAIChatViaAnthropicStream`）。
- [ ] Step 4: 重跑 PASS；`go test ./test/unit/llmproxy_usecase/...` 全绿。

### Task 5: E2E

**Files:**
- Create: `test/e2e/chat_to_response/chat_to_response_test.go`

**Interfaces:**
- Consumes: Task 1-4 全部；既有 e2e 骨架（真实 `RegisterAPIRouter` + sqlite + miniredis + httptest SSE 上游）。

- [ ] Step 1: 写四个用例（api_key 端点仅开 Responses 能力）：非流式+tools、流式、多轮工具循环（断言上游 input 含 function_call/function_call_output 且 call_id 对应）、Chat 能力端点 native 回归。
- [ ] Step 2: `go test ./test/e2e/chat_to_response/ -v` 迭代至全绿。

### Task 6: 文档与整体验证

**Files:**
- Modify: `CONTEXT.md`（Cross-Protocol Conversion、EndpointAuthType 两处）

- [ ] Step 1: 更新 CONTEXT 术语（7→8 条转换路径；oauth_openai 支持 Chat 跨协议转换）。
- [ ] Step 2: `go build ./...`、`go test ./test/unit/converter/... ./test/unit/llmproxy_usecase/... ./test/e2e/chat_to_response/...`、`go run ./cmd/lint conv ./...`、golangci-lint。
- [ ] Step 3: ponytail-review 审查 diff；向用户汇报并询问是否提 MR/合并。
