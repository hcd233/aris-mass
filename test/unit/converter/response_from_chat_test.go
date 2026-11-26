package converter

import (
	"testing"

	"github.com/bytedance/sonic"
	"github.com/samber/lo"

	"github.com/hcd233/aris-mass/internal/application/llmproxy/converter"
	proxyutil "github.com/hcd233/aris-mass/internal/application/llmproxy/util"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/dto"
	"github.com/hcd233/aris-mass/internal/dto/schema"
)

func chatTextMessage(role enum.Role, text string) *dto.OpenAIChatCompletionMessageParam {
	return &dto.OpenAIChatCompletionMessageParam{Role: role, Content: &dto.OpenAIMessageContent{Text: text}}
}

func findResponseItem(items []*dto.ResponseInputItem, itemType string) *dto.ResponseInputItem {
	for _, item := range items {
		if lo.FromPtr(item.Type) == itemType {
			return item
		}
	}
	return nil
}

// 多轮工具循环的完整历史：system/developer 合并为 instructions，
// assistant 的 reasoning 被丢弃、tool_calls 转 function_call，tool 消息转 function_call_output。
func TestToResponseRequest_ToolConversationHistory(t *testing.T) {
	t.Parallel()
	stream := false
	req := &dto.OpenAIChatCompletionReq{
		Model:  "alias",
		Stream: &stream,
		Messages: []*dto.OpenAIChatCompletionMessageParam{
			chatTextMessage(enum.RoleSystem, "sys"),
			chatTextMessage(enum.RoleDeveloper, "dev"),
			{
				Role: enum.RoleUser,
				Content: &dto.OpenAIMessageContent{Parts: []*dto.OpenAIChatCompletionContentPart{
					{Type: enum.ContentPartTypeText, Text: lo.ToPtr("看这图")},
					{Type: enum.ContentPartTypeImageURL, ImageURL: &dto.OpenAIChatCompletionImageURL{URL: "https://img", Detail: "high"}},
				}},
			},
			{
				Role:             enum.RoleAssistant,
				Content:          &dto.OpenAIMessageContent{Text: "我先查一下"},
				ReasoningContent: lo.ToPtr("internal thinking"),
				ToolCalls: []*dto.OpenAIChatCompletionMessageToolCall{{
					ID:       lo.ToPtr("call_1"),
					Type:     enum.ToolTypeFunction,
					Function: &dto.OpenAIChatCompletionMessageFunctionToolCall{Name: "get_weather", Arguments: `{"city":"SZ"}`},
				}},
			},
			{
				Role:       enum.RoleTool,
				ToolCallID: lo.ToPtr("call_1"),
				Content:    &dto.OpenAIMessageContent{Text: "sunny"},
			},
		},
	}

	got, err := (&converter.ResponseProtocolConverter{}).ToResponseRequest(req)
	if err != nil {
		t.Fatalf("ToResponseRequest() error: %v", err)
	}
	if lo.FromPtr(got.Instructions) != "sys\n\ndev" {
		t.Fatalf("instructions = %q, want %q", lo.FromPtr(got.Instructions), "sys\n\ndev")
	}
	if !lo.FromPtr(got.Stream) {
		t.Fatal("stream = false, want true（统一上游流式）")
	}
	if got.Input == nil || len(got.Input.Items) != 4 {
		t.Fatalf("input items = %v, want 4 items", got.Input)
	}
	items := got.Input.Items

	user := items[0]
	if lo.FromPtr(user.Role) != enum.RoleUser || user.Content == nil || len(user.Content.Parts) != 2 {
		t.Fatalf("user item = %+v, want 2 content parts", user)
	}
	if user.Content.Parts[0].Type != enum.ResponseContentTypeInputText || lo.FromPtr(user.Content.Parts[0].Text) != "看这图" {
		t.Fatalf("user text part = %+v", user.Content.Parts[0])
	}
	if user.Content.Parts[1].Type != enum.ResponseContentTypeInputImage || lo.FromPtr(user.Content.Parts[1].ImageURL) != "https://img" {
		t.Fatalf("user image part = %+v", user.Content.Parts[1])
	}

	if reasoning := findResponseItem(items, enum.ResponseInputItemTypeReasoning); reasoning != nil {
		t.Fatalf("reasoning item = %+v, want dropped", reasoning)
	}
	call := findResponseItem(items, enum.ResponseInputItemTypeFunctionCall)
	if call == nil {
		t.Fatal("function_call item not found")
	}
	if lo.FromPtr(call.CallID) != "call_1" || lo.FromPtr(call.Name) != "get_weather" || lo.FromPtr(call.Arguments) != `{"city":"SZ"}` {
		t.Fatalf("function_call = %+v", call)
	}
	var assistantText *dto.ResponseInputItem
	for _, item := range items {
		if lo.FromPtr(item.Type) == enum.ResponseInputItemTypeMessage && lo.FromPtr(item.Role) == enum.RoleAssistant {
			assistantText = item
		}
	}
	if assistantText == nil || assistantText.Content == nil || len(assistantText.Content.Parts) != 1 {
		t.Fatalf("assistant message item = %+v", assistantText)
	}
	if assistantText.Content.Parts[0].Type != enum.ResponseContentTypeOutputText || lo.FromPtr(assistantText.Content.Parts[0].Text) != "我先查一下" {
		t.Fatalf("assistant text part = %+v", assistantText.Content.Parts[0])
	}
	output := findResponseItem(items, enum.ResponseInputItemTypeFunctionCallOutput)
	if output == nil || lo.FromPtr(output.CallID) != "call_1" {
		t.Fatalf("function_call_output = %+v", output)
	}
	if output.Output == nil || output.Output.Text != "sunny" {
		t.Fatalf("function_call_output.output = %+v, want sunny", output.Output)
	}
}

func TestToResponseRequest_ScalarAndToolMapping(t *testing.T) {
	t.Parallel()
	req := &dto.OpenAIChatCompletionReq{
		Model:               "alias",
		Messages:            []*dto.OpenAIChatCompletionMessageParam{chatTextMessage(enum.RoleUser, "hi")},
		MaxCompletionTokens: lo.ToPtr(1024),
		ReasoningEffort:     "high",
		Verbosity:           "low",
		Metadata:            map[string]string{"k": "v"},
		ParallelToolCalls:   lo.ToPtr(false),
		PromptCacheKey:      lo.ToPtr("pck"),
		ResponseFormat: &dto.OpenAIResponseFormat{
			Type: "json_schema",
			JSONSchema: &dto.OpenAIJSONSchemaFormat{
				Name:   "answer",
				Strict: lo.ToPtr(true),
				Schema: &schema.JSONSchemaProperty{},
			},
		},
		Tools: []dto.OpenAIChatCompletionTool{
			{Type: enum.ToolTypeFunction, Function: &dto.OpenAIFunctionDefinition{Name: "f", Description: lo.ToPtr("d"), Strict: lo.ToPtr(true)}},
			{Type: enum.ToolTypeCustom, Custom: &dto.OpenAICustomToolDefinition{Name: "c"}},
		},
		ToolChoice: &dto.OpenAIChatCompletionToolChoiceParam{
			Named: &dto.OpenAIChatCompletionToolChoice{Type: enum.ToolTypeFunction, Function: &dto.OpenAIToolChoiceFunction{Name: "f"}},
		},
	}

	got, err := (&converter.ResponseProtocolConverter{}).ToResponseRequest(req)
	if err != nil {
		t.Fatalf("ToResponseRequest() error: %v", err)
	}
	if lo.FromPtr(got.MaxOutputTokens) != 1024 {
		t.Fatalf("max_output_tokens = %v", got.MaxOutputTokens)
	}
	if got.Reasoning == nil || lo.FromPtr(got.Reasoning.Effort) != "high" {
		t.Fatalf("reasoning = %+v", got.Reasoning)
	}
	if got.Text == nil || lo.FromPtr(got.Text.Verbosity) != "low" {
		t.Fatalf("text = %+v", got.Text)
	}
	if got.Text.Format == nil || got.Text.Format.Type != "json_schema" || lo.FromPtr(got.Text.Format.Name) != "answer" || !lo.FromPtr(got.Text.Format.Strict) {
		t.Fatalf("text.format = %+v", got.Text.Format)
	}
	if got.Metadata["k"] != "v" || lo.FromPtr(got.ParallelToolCalls) || lo.FromPtr(got.PromptCacheKey) != "pck" {
		t.Fatalf("scalar mapping = %+v", got)
	}
	if len(got.Tools) != 2 || got.Tools[0].Function == nil || got.Tools[0].Function.Name != "f" || got.Tools[1].Custom == nil || got.Tools[1].Custom.Name != "c" {
		t.Fatalf("tools = %+v", got.Tools)
	}
	if got.ToolChoice == nil || got.ToolChoice.Object == nil || got.ToolChoice.Object.Type != enum.ResponseToolChoiceTypeFunction || lo.FromPtr(got.ToolChoice.Object.Name) != "f" {
		t.Fatalf("tool_choice = %+v", got.ToolChoice)
	}
}

func TestToResponseRequest_MaxTokensFallbackAndEmptyInstructions(t *testing.T) {
	t.Parallel()
	req := &dto.OpenAIChatCompletionReq{
		Model:     "alias",
		MaxTokens: lo.ToPtr(256),
		Messages:  []*dto.OpenAIChatCompletionMessageParam{chatTextMessage(enum.RoleUser, "hi")},
	}
	got, err := (&converter.ResponseProtocolConverter{}).ToResponseRequest(req)
	if err != nil {
		t.Fatalf("ToResponseRequest() error: %v", err)
	}
	if lo.FromPtr(got.MaxOutputTokens) != 256 {
		t.Fatalf("max_output_tokens = %v, want 256（max_tokens 回退）", got.MaxOutputTokens)
	}
	if got.Instructions == nil || *got.Instructions != "" {
		t.Fatalf("instructions = %v, want 非 nil 空串", got.Instructions)
	}
}

func TestToResponseRequest_Store(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		store    *bool
		expected bool
	}{
		{name: "omitted", expected: false},
		{name: "explicit false", store: lo.ToPtr(false), expected: false},
		{name: "explicit true", store: lo.ToPtr(true), expected: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := &dto.OpenAIChatCompletionReq{
				Model:    "alias",
				Messages: []*dto.OpenAIChatCompletionMessageParam{chatTextMessage(enum.RoleUser, "hi")},
				Store:    tt.store,
			}
			got, err := (&converter.ResponseProtocolConverter{}).ToResponseRequest(req)
			if err != nil {
				t.Fatalf("ToResponseRequest() error: %v", err)
			}
			body := proxyutil.MarshalOpenAIResponseBodyForModel(got, "upstream")
			var upstream dto.OpenAICreateResponseReq
			if err := sonic.Unmarshal(body, &upstream); err != nil {
				t.Fatalf("decode Responses upstream request: %v", err)
			}
			if upstream.Store == nil || *upstream.Store != tt.expected {
				t.Fatalf("upstream body = %s, want store=%t", body, tt.expected)
			}
		})
	}
}

func TestToResponseRequest_RejectsMultipleChoices(t *testing.T) {
	t.Parallel()
	req := &dto.OpenAIChatCompletionReq{
		Model:    "alias",
		N:        lo.ToPtr(2),
		Messages: []*dto.OpenAIChatCompletionMessageParam{chatTextMessage(enum.RoleUser, "hi")},
	}
	if _, err := (&converter.ResponseProtocolConverter{}).ToResponseRequest(req); err == nil {
		t.Fatal("n=2 error = nil, want 不支持多候选")
	}
}

// Chat custom 工具的历史调用要还原为 Responses custom_tool_call（arguments JSON 拆包）。
func TestToResponseRequest_CustomToolHistoryUnwrapped(t *testing.T) {
	t.Parallel()
	req := &dto.OpenAIChatCompletionReq{
		Model: "alias",
		Tools: []dto.OpenAIChatCompletionTool{
			{Type: enum.ToolTypeCustom, Custom: &dto.OpenAICustomToolDefinition{Name: "apply_patch"}},
		},
		Messages: []*dto.OpenAIChatCompletionMessageParam{
			chatTextMessage(enum.RoleUser, "hi"),
			{
				Role: enum.RoleAssistant,
				ToolCalls: []*dto.OpenAIChatCompletionMessageToolCall{{
					ID:       lo.ToPtr("call_9"),
					Type:     enum.ToolTypeFunction,
					Function: &dto.OpenAIChatCompletionMessageFunctionToolCall{Name: "apply_patch", Arguments: `{"content":"patch-body"}`},
				}},
			},
			{
				Role:       enum.RoleTool,
				ToolCallID: lo.ToPtr("call_9"),
				Content:    &dto.OpenAIMessageContent{Text: "applied"},
			},
		},
	}
	conv := &converter.ResponseProtocolConverter{}
	got, err := conv.ToResponseRequest(req)
	if err != nil {
		t.Fatalf("ToResponseRequest() error: %v", err)
	}
	item := findResponseItem(got.Input.Items, enum.ResponseInputItemTypeCustomToolCall)
	if item == nil {
		t.Fatalf("custom_tool_call item not found: %+v", got.Input.Items)
	}
	if lo.FromPtr(item.CallID) != "call_9" || lo.FromPtr(item.Input) != "patch-body" {
		t.Fatalf("custom_tool_call = %+v", item)
	}
	output := findResponseItem(got.Input.Items, enum.ResponseInputItemTypeCustomToolCallOutput)
	if output == nil || lo.FromPtr(output.CallID) != "call_9" || output.Output == nil || output.Output.Text != "applied" {
		t.Errorf("custom_tool_call_output = %+v, want call_9 with output applied", output)
	}

	req.Tools = nil
	got, err = conv.ToResponseRequest(req)
	if err != nil {
		t.Fatalf("ToResponseRequest() without tools error: %v", err)
	}
	if findResponseItem(got.Input.Items, enum.ResponseInputItemTypeFunctionCall) == nil {
		t.Error("function call inherited custom tool definition from previous request")
	}
}

func TestToResponseRequest_CustomToolHistoryWithoutDefinitions(t *testing.T) {
	t.Parallel()
	input := `{"content":"preserve this raw input"}`
	req := &dto.OpenAIChatCompletionReq{
		Model: "alias",
		Messages: []*dto.OpenAIChatCompletionMessageParam{
			{
				Role: enum.RoleAssistant,
				ToolCalls: []*dto.OpenAIChatCompletionMessageToolCall{
					{
						ID:     lo.ToPtr("call_custom"),
						Type:   enum.ToolTypeCustom,
						Custom: &dto.OpenAIChatCompletionMessageCustomToolCall{Name: "format_note", Input: input},
					},
					{
						ID:       lo.ToPtr("call_function"),
						Type:     enum.ToolTypeFunction,
						Function: &dto.OpenAIChatCompletionMessageFunctionToolCall{Name: "format_note", Arguments: `{}`},
					},
				},
			},
			{
				Role:       enum.RoleTool,
				ToolCallID: lo.ToPtr("call_function"),
				Content:    &dto.OpenAIMessageContent{Text: "function result"},
			},
			{
				Role:       enum.RoleTool,
				ToolCallID: lo.ToPtr("call_custom"),
				Content: &dto.OpenAIMessageContent{Parts: []*dto.OpenAIChatCompletionContentPart{
					{Type: enum.ContentPartTypeText, Text: lo.ToPtr("custom result")},
				}},
			},
		},
	}

	got, err := (&converter.ResponseProtocolConverter{}).ToResponseRequest(req)
	if err != nil {
		t.Fatalf("ToResponseRequest() error: %v", err)
	}
	items := got.Input.Items
	if len(items) != 4 {
		t.Fatalf("input has %d items, want 4", len(items))
	}
	customCall, functionCall, functionOutput, customOutput := items[0], items[1], items[2], items[3]
	if lo.FromPtr(customCall.Type) != enum.ResponseInputItemTypeCustomToolCall ||
		lo.FromPtr(customCall.CallID) != "call_custom" || lo.FromPtr(customCall.Input) != input {
		t.Errorf("custom call type=%q call_id=%q input=%q, want custom_tool_call on call_custom with input %s",
			lo.FromPtr(customCall.Type), lo.FromPtr(customCall.CallID), lo.FromPtr(customCall.Input), input)
	}
	if lo.FromPtr(functionCall.Type) != enum.ResponseInputItemTypeFunctionCall ||
		lo.FromPtr(functionCall.CallID) != "call_function" || lo.FromPtr(functionCall.Arguments) != `{}` {
		t.Errorf("function call = %+v, want JSON arguments on call_function", functionCall)
	}
	if lo.FromPtr(functionOutput.Type) != enum.ResponseInputItemTypeFunctionCallOutput ||
		lo.FromPtr(functionOutput.CallID) != "call_function" || functionOutput.Output == nil ||
		functionOutput.Output.Text != "function result" {
		t.Errorf("function output = %+v, want function result on call_function", functionOutput)
	}
	if lo.FromPtr(customOutput.Type) != enum.ResponseInputItemTypeCustomToolCallOutput ||
		lo.FromPtr(customOutput.CallID) != "call_custom" || customOutput.Output == nil ||
		customOutput.Output.FunctionOutput == nil {
		t.Fatalf("custom output type=%q call_id=%q output=%+v, want structured custom result on call_custom",
			lo.FromPtr(customOutput.Type), lo.FromPtr(customOutput.CallID), customOutput.Output)
	}
	parts := customOutput.Output.FunctionOutput.Parts
	if len(parts) != 1 || parts[0].Type != enum.ResponseContentTypeInputText || lo.FromPtr(parts[0].Text) != "custom result" {
		t.Fatalf("custom output parts = %+v, want input_text custom result", parts)
	}
}
