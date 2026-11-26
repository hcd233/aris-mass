package converter

import (
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/samber/lo"

	proxyutil "github.com/hcd233/aris-mass/internal/application/llmproxy/util"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/common/ierr"
	"github.com/hcd233/aris-mass/internal/dto"
)

// ResponseToChatStreamConverter 将上游 Responses SSE 事件流转换为 Chat Completions chunk。
// WriteResponseDeltaFromChatChunk 的逆方向，供 CompatRouteViaOpenAIResponse 使用。
//
// 状态机要点：
//   - function_call/custom_tool_call 以 output_item.added 的 item_id 分配稳定的
//     Chat tool_call index（支持并行调用、乱序 arguments 增量）；
//   - 终态 response 对象在 completed/incomplete/failed 记录，output 为空时用
//     output_item.done 累积的 items 补齐（与 native Response 流同一兜底策略）。
type ResponseToChatStreamConverter struct {
	exposedModel string
	chunkID      string

	toolCallIDs   map[int]string
	itemIndexes   map[string]int
	nextToolIndex int
	hasToolCall   bool
	roleWritten   bool

	accumulatedOutput []*dto.ResponseInputItem
	finalResponse     *dto.OpenAICreateResponseRsp
}

func NewResponseToChatStreamConverter(exposedModel string) *ResponseToChatStreamConverter {
	return &ResponseToChatStreamConverter{
		exposedModel: exposedModel,
		chunkID:      fmt.Sprintf(constant.OpenAIChunkIDTemplate, constant.ConvertedChunkIDSuffix),
		toolCallIDs:  make(map[int]string),
		itemIndexes:  make(map[string]int),
	}
}

// responseStreamDeltaEvent Responses 增量事件的公共载荷（delta + item_id）。
type responseStreamDeltaEvent struct {
	ItemID string `json:"item_id"`
	Delta  string `json:"delta"`
}

// HandleEvent 消费一个上游 SSE 事件，产出 0..n 个 Chat chunk。
func (c *ResponseToChatStreamConverter) HandleEvent(event string, data []byte) ([]*dto.OpenAIChatCompletionChunk, error) {
	switch event {
	case enum.ResponseStreamEventOutputTextDelta,
		enum.ResponseStreamEventReasoningTextDelta,
		enum.ResponseStreamEventReasoningSummaryTextDelta:
		return c.handleTextDelta(event, data)
	case enum.ResponseStreamEventFunctionCallArgumentsDelta:
		return c.handleToolArgumentDelta(data, false)
	case enum.ResponseStreamEventCustomToolCallInputDelta:
		return c.handleToolArgumentDelta(data, true)
	case enum.ResponseStreamEventOutputItemAdded:
		return c.handleOutputItemAdded(data)
	case enum.ResponseStreamEventOutputItemDone:
		return c.handleOutputItemDone(data)
	case enum.ResponseStreamEventCompleted,
		enum.ResponseStreamEventIncomplete,
		enum.ResponseStreamEventFailed:
		return c.handleTerminalEvent(event, data)
	}
	return nil, nil
}

// FinalResponse 返回终态 Responses 响应（审计 usage 与失败判定用）；未到达终态时为 nil。
func (c *ResponseToChatStreamConverter) FinalResponse() *dto.OpenAICreateResponseRsp {
	return c.finalResponse
}

func (c *ResponseToChatStreamConverter) newChunk() *dto.OpenAIChatCompletionChunk {
	return &dto.OpenAIChatCompletionChunk{
		ID:      c.chunkID,
		Object:  enum.CompletionObjectChatCompletionChunk,
		Created: time.Now().Unix(),
		Model:   c.exposedModel,
		Choices: []*dto.OpenAIChatCompletionChunkChoice{{Index: 0, Delta: &dto.OpenAIChatCompletionChunkDelta{}}},
	}
}

func (c *ResponseToChatStreamConverter) markRole(chunk *dto.OpenAIChatCompletionChunk) {
	if c.roleWritten {
		return
	}
	c.roleWritten = true
	chunk.Choices[0].Delta.Role = enum.RoleAssistant
}

func (c *ResponseToChatStreamConverter) handleTextDelta(event string, data []byte) ([]*dto.OpenAIChatCompletionChunk, error) {
	var ev responseStreamDeltaEvent
	if err := sonic.Unmarshal(data, &ev); err != nil {
		return nil, ierr.Wrap(ierr.ErrSSEParse, err, "unmarshal response delta event")
	}
	if ev.Delta == "" {
		return nil, nil
	}
	chunk := c.newChunk()
	if event == enum.ResponseStreamEventOutputTextDelta {
		chunk.Choices[0].Delta.Content = lo.ToPtr(ev.Delta)
	} else {
		chunk.Choices[0].Delta.ReasoningContent = lo.ToPtr(ev.Delta)
	}
	c.markRole(chunk)
	return []*dto.OpenAIChatCompletionChunk{chunk}, nil
}

// resolveToolIndex 按 item_id 取 Chat tool_call index；未见 added 事件时兜底分配。
func (c *ResponseToChatStreamConverter) resolveToolIndex(itemID string) int {
	if idx, ok := c.itemIndexes[itemID]; ok {
		return idx
	}
	idx := c.nextToolIndex
	c.nextToolIndex++
	c.itemIndexes[itemID] = idx
	return idx
}

func (c *ResponseToChatStreamConverter) handleToolArgumentDelta(data []byte, isCustom bool) ([]*dto.OpenAIChatCompletionChunk, error) {
	var ev responseStreamDeltaEvent
	if err := sonic.Unmarshal(data, &ev); err != nil {
		return nil, ierr.Wrap(ierr.ErrSSEParse, err, "unmarshal tool argument delta event")
	}
	if ev.Delta == "" {
		return nil, nil
	}
	idx := c.resolveToolIndex(ev.ItemID)
	tc := &dto.OpenAIChatCompletionMessageToolCall{Index: lo.ToPtr(idx)}
	if isCustom {
		tc.Custom = &dto.OpenAIChatCompletionMessageCustomToolCall{Input: ev.Delta}
	} else {
		tc.Function = &dto.OpenAIChatCompletionMessageFunctionToolCall{Arguments: ev.Delta}
	}
	chunk := c.newChunk()
	chunk.Choices[0].Delta.ToolCalls = []*dto.OpenAIChatCompletionMessageToolCall{tc}
	c.markRole(chunk)
	c.normalize(chunk)
	return []*dto.OpenAIChatCompletionChunk{chunk}, nil
}

func (c *ResponseToChatStreamConverter) handleOutputItemAdded(data []byte) ([]*dto.OpenAIChatCompletionChunk, error) {
	// added 与 done 事件同形（output_index + item），复用既有 DTO。
	var ev dto.ResponseStreamOutputItemDoneEvent
	if err := sonic.Unmarshal(data, &ev); err != nil {
		return nil, ierr.Wrap(ierr.ErrSSEParse, err, "unmarshal output_item.added event")
	}
	if ev.Item == nil {
		return nil, nil
	}
	itemType := lo.FromPtr(ev.Item.Type)
	if itemType != enum.ResponseInputItemTypeFunctionCall && itemType != enum.ResponseInputItemTypeCustomToolCall {
		return nil, nil
	}
	c.hasToolCall = true
	idx := c.resolveToolIndex(lo.FromPtr(ev.Item.ID))
	callID := lo.FromPtr(ev.Item.CallID)
	if callID == "" {
		callID = lo.FromPtr(ev.Item.ID)
	}
	tc := &dto.OpenAIChatCompletionMessageToolCall{Index: lo.ToPtr(idx), ID: lo.ToPtr(callID)}
	if itemType == enum.ResponseInputItemTypeCustomToolCall {
		tc.Type = enum.ToolTypeCustom
		tc.Custom = &dto.OpenAIChatCompletionMessageCustomToolCall{Name: lo.FromPtr(ev.Item.Name)}
	} else {
		tc.Type = enum.ToolTypeFunction
		tc.Function = &dto.OpenAIChatCompletionMessageFunctionToolCall{Name: lo.FromPtr(ev.Item.Name)}
	}
	chunk := c.newChunk()
	chunk.Choices[0].Delta.ToolCalls = []*dto.OpenAIChatCompletionMessageToolCall{tc}
	c.markRole(chunk)
	c.normalize(chunk)
	return []*dto.OpenAIChatCompletionChunk{chunk}, nil
}

func (c *ResponseToChatStreamConverter) handleOutputItemDone(data []byte) ([]*dto.OpenAIChatCompletionChunk, error) {
	var ev dto.ResponseStreamOutputItemDoneEvent
	if err := sonic.Unmarshal(data, &ev); err != nil {
		return nil, ierr.Wrap(ierr.ErrSSEParse, err, "unmarshal output_item.done event")
	}
	if ev.Item == nil {
		return nil, nil
	}
	c.accumulatedOutput = append(c.accumulatedOutput, ev.Item)
	if ev.Item.Content == nil {
		return nil, nil
	}
	// message item 的 refusal part → Chat refusal delta。
	var chunks []*dto.OpenAIChatCompletionChunk
	for _, part := range ev.Item.Content.Parts {
		if part == nil || part.Type != enum.ResponseContentTypeRefusal || lo.FromPtr(part.Refusal) == "" {
			continue
		}
		chunk := c.newChunk()
		chunk.Choices[0].Delta.Refusal = part.Refusal
		c.markRole(chunk)
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

func (c *ResponseToChatStreamConverter) handleTerminalEvent(event string, data []byte) ([]*dto.OpenAIChatCompletionChunk, error) {
	var ev dto.ResponseStreamTerminalEvent
	if err := sonic.Unmarshal(data, &ev); err != nil {
		return nil, ierr.Wrap(ierr.ErrSSEParse, err, "unmarshal response terminal event")
	}
	rsp := ev.Response
	if rsp != nil && len(rsp.Output) == 0 && len(c.accumulatedOutput) > 0 {
		rsp.Output = c.accumulatedOutput
	}
	c.finalResponse = rsp
	if event == enum.ResponseStreamEventFailed || rsp == nil {
		return nil, nil
	}

	chunk := c.newChunk()
	finishReason := c.finishReason(event, rsp)
	chunk.Choices[0].FinishReason = lo.ToPtr(finishReason)
	chunk.Usage = responseUsageToChat(rsp.Usage)
	return []*dto.OpenAIChatCompletionChunk{chunk}, nil
}

func (c *ResponseToChatStreamConverter) finishReason(event string, rsp *dto.OpenAICreateResponseRsp) enum.FinishReason {
	if event == enum.ResponseStreamEventIncomplete {
		if rsp.IncompleteDetails != nil && rsp.IncompleteDetails.Reason == enum.ResponseIncompleteReasonContentFilter {
			return enum.FinishReasonContentFilter
		}
		return enum.FinishReasonLength
	}
	if c.hasToolCall {
		return enum.FinishReasonToolCalls
	}
	return enum.FinishReasonStop
}

// normalize 复用 native Chat 流的 tool_call 规范化：每个 chunk 都带 id/index。
func (c *ResponseToChatStreamConverter) normalize(chunk *dto.OpenAIChatCompletionChunk) {
	proxyutil.NormalizeOpenAIStreamToolCalls(chunk, c.toolCallIDs)
}

// responseUsageToChat 将 Responses usage 映射为 Chat usage（含缓存与推理明细）。
func responseUsageToChat(usage *dto.ResponseUsage) *dto.OpenAICompletionUsage {
	if usage == nil {
		return nil
	}
	out := &dto.OpenAICompletionUsage{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      usage.TotalTokens,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	if usage.InputTokensDetails != nil {
		out.PromptTokensDetails = &dto.OpenAIPromptTokensDetails{CachedTokens: lo.ToPtr(usage.InputTokensDetails.CachedTokens)}
	}
	if usage.OutputTokensDetails != nil {
		out.CompletionTokensDetails = &dto.OpenAICompletionTokensDetails{ReasoningTokens: lo.ToPtr(usage.OutputTokensDetails.ReasoningTokens)}
	}
	return out
}
