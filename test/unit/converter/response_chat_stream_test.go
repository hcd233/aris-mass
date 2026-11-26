package converter

import (
	"testing"

	"github.com/samber/lo"

	"github.com/hcd233/aris-mass/internal/application/llmproxy/converter"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/dto"
)

func feedEvents(t *testing.T, c *converter.ResponseToChatStreamConverter, events [][2]string) []*dto.OpenAIChatCompletionChunk {
	t.Helper()
	var chunks []*dto.OpenAIChatCompletionChunk
	for _, ev := range events {
		got, err := c.HandleEvent(ev[0], []byte(ev[1]))
		if err != nil {
			t.Fatalf("HandleEvent(%s) error: %v", ev[0], err)
		}
		chunks = append(chunks, got...)
	}
	return chunks
}

func TestResponseToChatStream_TextAndReasoning(t *testing.T) {
	t.Parallel()
	c := converter.NewResponseToChatStreamConverter("alias")
	chunks := feedEvents(t, c, [][2]string{
		{enum.ResponseStreamEventOutputTextDelta, `{"item_id":"msg_1","delta":"Hel"}`},
		{enum.ResponseStreamEventReasoningTextDelta, `{"item_id":"rs_1","delta":"think"}`},
		{enum.ResponseStreamEventReasoningSummaryTextDelta, `{"item_id":"rs_1","delta":"sum"}`},
		{enum.ResponseStreamEventOutputTextDelta, `{"item_id":"msg_1","delta":"lo"}`},
	})
	if len(chunks) != 4 {
		t.Fatalf("chunks = %d, want 4", len(chunks))
	}
	if lo.FromPtr(chunks[0].Choices[0].Delta.Content) != "Hel" || chunks[0].Choices[0].Delta.Role != enum.RoleAssistant {
		t.Fatalf("first chunk = %+v, want content Hel + role assistant", chunks[0].Choices[0].Delta)
	}
	if lo.FromPtr(chunks[1].Choices[0].Delta.ReasoningContent) != "think" {
		t.Fatalf("reasoning chunk = %+v", chunks[1].Choices[0].Delta)
	}
	if lo.FromPtr(chunks[2].Choices[0].Delta.ReasoningContent) != "sum" {
		t.Fatalf("reasoning summary chunk = %+v", chunks[2].Choices[0].Delta)
	}
	if chunks[3].Choices[0].Delta.Role != "" {
		t.Fatalf("role = %q, want 仅首帧携带", chunks[3].Choices[0].Delta.Role)
	}
	if chunks[0].Model != "alias" || chunks[0].Object != enum.CompletionObjectChatCompletionChunk {
		t.Fatalf("chunk meta = %+v", chunks[0])
	}
}

func TestResponseToChatStream_ParallelFunctionCalls(t *testing.T) {
	t.Parallel()
	c := converter.NewResponseToChatStreamConverter("alias")
	chunks := feedEvents(t, c, [][2]string{
		{enum.ResponseStreamEventOutputItemAdded, `{"output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"a"}}`},
		{enum.ResponseStreamEventOutputItemAdded, `{"output_index":1,"item":{"id":"fc_2","type":"function_call","call_id":"call_2","name":"b"}}`},
		{enum.ResponseStreamEventFunctionCallArgumentsDelta, `{"item_id":"fc_2","delta":"B"}`},
		{enum.ResponseStreamEventFunctionCallArgumentsDelta, `{"item_id":"fc_1","delta":"A"}`},
		{enum.ResponseStreamEventCompleted, `{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":4}}}}`},
	})
	if len(chunks) != 5 {
		t.Fatalf("chunks = %d, want 5", len(chunks))
	}
	first := chunks[0].Choices[0].Delta.ToolCalls[0]
	second := chunks[1].Choices[0].Delta.ToolCalls[0]
	if lo.FromPtr(first.Index) != 0 || lo.FromPtr(first.ID) != "call_1" || first.Function.Name != "a" {
		t.Fatalf("first tool call = %+v", first)
	}
	if lo.FromPtr(second.Index) != 1 || lo.FromPtr(second.ID) != "call_2" || second.Function.Name != "b" {
		t.Fatalf("second tool call = %+v", second)
	}
	argB := chunks[2].Choices[0].Delta.ToolCalls[0]
	if lo.FromPtr(argB.Index) != 1 || argB.Function.Arguments != "B" || lo.FromPtr(argB.ID) != "call_2" {
		t.Fatalf("args delta for fc_2 = %+v, want index 1 + id 回填", argB)
	}
	argA := chunks[3].Choices[0].Delta.ToolCalls[0]
	if lo.FromPtr(argA.Index) != 0 || argA.Function.Arguments != "A" {
		t.Fatalf("args delta for fc_1 = %+v", argA)
	}

	final := chunks[4]
	if lo.FromPtr(final.Choices[0].FinishReason) != enum.FinishReasonToolCalls {
		t.Fatalf("finish_reason = %v, want tool_calls", final.Choices[0].FinishReason)
	}
	usage := final.Usage
	if usage == nil || usage.PromptTokens != 10 || usage.CompletionTokens != 5 || usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", usage)
	}
	if lo.FromPtr(usage.PromptTokensDetails.CachedTokens) != 3 || lo.FromPtr(usage.CompletionTokensDetails.ReasoningTokens) != 4 {
		t.Fatalf("usage details = %+v", usage)
	}
	if c.FinalResponse() == nil || c.FinalResponse().ID != "resp_1" {
		t.Fatalf("FinalResponse = %+v", c.FinalResponse())
	}
}

func TestResponseToChatStream_CompletedWithoutToolsFinishStop(t *testing.T) {
	t.Parallel()
	c := converter.NewResponseToChatStreamConverter("alias")
	chunks := feedEvents(t, c, [][2]string{
		{enum.ResponseStreamEventCompleted, `{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`},
	})
	if len(chunks) != 1 || lo.FromPtr(chunks[0].Choices[0].FinishReason) != enum.FinishReasonStop {
		t.Fatalf("chunks = %+v, want finish_reason stop", chunks)
	}
}

func TestResponseToChatStream_IncompleteFinishReasons(t *testing.T) {
	t.Parallel()
	cases := map[string]enum.FinishReason{
		"max_output_tokens": enum.FinishReasonLength,
		"content_filter":    enum.FinishReasonContentFilter,
	}
	for reason, want := range cases {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			c := converter.NewResponseToChatStreamConverter("alias")
			chunks := feedEvents(t, c, [][2]string{
				{enum.ResponseStreamEventIncomplete, `{"type":"response.incomplete","response":{"id":"resp_1","status":"incomplete","incomplete_details":{"reason":"` + reason + `"}}}`},
			})
			if len(chunks) != 1 || lo.FromPtr(chunks[0].Choices[0].FinishReason) != want {
				t.Fatalf("chunks = %+v, want finish_reason %v", chunks, want)
			}
		})
	}
}

func TestResponseToChatStream_FailedProducesNoFinalChunk(t *testing.T) {
	t.Parallel()
	c := converter.NewResponseToChatStreamConverter("alias")
	chunks := feedEvents(t, c, [][2]string{
		{enum.ResponseStreamEventFailed, `{"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"server_error","message":"boom"}}}`},
	})
	if len(chunks) != 0 {
		t.Fatalf("chunks = %+v, want 空（失败走错误帧）", chunks)
	}
	rsp := c.FinalResponse()
	if rsp == nil || rsp.Status != "failed" || rsp.Error == nil || rsp.Error.Message != "boom" {
		t.Fatalf("FinalResponse = %+v", rsp)
	}
}

func TestResponseToChatStream_TerminalOutputBackfilled(t *testing.T) {
	t.Parallel()
	c := converter.NewResponseToChatStreamConverter("alias")
	_ = feedEvents(t, c, [][2]string{
		{enum.ResponseStreamEventOutputItemDone, `{"output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}`},
		{enum.ResponseStreamEventCompleted, `{"type":"response.completed","response":{"id":"resp_1","status":"completed"}}`},
	})
	rsp := c.FinalResponse()
	if rsp == nil || len(rsp.Output) != 1 || lo.FromPtr(rsp.Output[0].ID) != "msg_1" {
		t.Fatalf("FinalResponse.Output = %+v, want 由 output_item.done 补齐", rsp)
	}
}

func TestResponseToChatStream_RefusalFromMessageItem(t *testing.T) {
	t.Parallel()
	c := converter.NewResponseToChatStreamConverter("alias")
	chunks := feedEvents(t, c, [][2]string{
		{enum.ResponseStreamEventOutputItemDone, `{"output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"refusal","refusal":"cannot"}]}}`},
	})
	if len(chunks) != 1 || lo.FromPtr(chunks[0].Choices[0].Delta.Refusal) != "cannot" {
		t.Fatalf("chunks = %+v, want refusal delta", chunks)
	}
}

func TestResponseToChatStream_IgnoresMetadataEvents(t *testing.T) {
	t.Parallel()
	c := converter.NewResponseToChatStreamConverter("alias")
	chunks := feedEvents(t, c, [][2]string{
		{enum.ResponseStreamEventCreated, `{"type":"response.created","response":{"id":"resp_1"}}`},
		{enum.ResponseStreamEventInProgress, `{"type":"response.in_progress","response":{"id":"resp_1"}}`},
		{enum.ResponseStreamEventContentPartAdded, `{"item_id":"msg_1","part":{"type":"output_text","text":""}}`},
		{enum.ResponseStreamEventOutputTextDone, `{"item_id":"msg_1","text":"ok"}`},
	})
	if len(chunks) != 0 {
		t.Fatalf("chunks = %+v, want 忽略元数据事件", chunks)
	}
}
