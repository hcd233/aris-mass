package converter

import (
	"strings"

	"github.com/samber/lo"

	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/common/ierr"
	"github.com/hcd233/aris-mass/internal/dto"
)

// ToResponseRequest 将 Chat Completions 请求转换为 Responses 请求（CompatRouteViaOpenAIResponse）。
//
// 与 FromResponseRequest 互为逆方向，共用同一套 DTO 与工具映射：
//   - system/developer 合并为 instructions（无则给空串，保证字段存在）；
//   - assistant 历史丢弃 reasoning（对称于 Response→Chat 丢弃 reasoning item）；
//   - tool 消息转 function_call_output，call_id 原样保留（多轮工具循环闭环）；
//   - stream 恒为 true：上游统一走 SSE，客户端非流式由 usecase 聚合。
func (c *ResponseProtocolConverter) ToResponseRequest(req *dto.OpenAIChatCompletionReq) (*dto.OpenAICreateResponseReq, error) {
	if req == nil {
		return nil, ierr.New(ierr.ErrDTOConvert, "openai chat completion request is nil")
	}
	if lo.FromPtr(req.N) > 1 {
		return nil, ierr.New(ierr.ErrDTOConvert, "response api does not support multiple choices")
	}

	out := &dto.OpenAICreateResponseReq{
		Model:             lo.ToPtr(req.Model),
		Stream:            lo.ToPtr(true),
		Temperature:       req.Temperature,
		TopP:              req.TopP,
		TopLogprobs:       req.TopLogprobs,
		Metadata:          req.Metadata,
		ParallelToolCalls: req.ParallelToolCalls,
		PromptCacheKey:    req.PromptCacheKey,
		SafetyIdentifier:  req.SafetyIdentifier,
		Store:             req.Store,
		User:              req.User,
	}
	if req.MaxCompletionTokens != nil {
		out.MaxOutputTokens = lo.ToPtr(int64(*req.MaxCompletionTokens))
	} else if req.MaxTokens != nil {
		out.MaxOutputTokens = lo.ToPtr(int64(*req.MaxTokens))
	}
	if req.PromptCacheRetention != "" {
		out.PromptCacheRetention = lo.ToPtr(req.PromptCacheRetention)
	}
	if req.ServiceTier != "" {
		out.ServiceTier = lo.ToPtr(req.ServiceTier)
	}
	if req.ReasoningEffort != "" {
		out.Reasoning = &dto.ResponseReasoningConfig{Effort: lo.ToPtr(req.ReasoningEffort)}
	}
	if req.Verbosity != "" || req.ResponseFormat != nil {
		out.Text = &dto.ResponseTextConfig{Format: chatResponseFormatToResponseText(req.ResponseFormat)}
		if req.Verbosity != "" {
			out.Text.Verbosity = lo.ToPtr(req.Verbosity)
		}
	}

	if len(req.Tools) > 0 {
		out.Tools = chatToolsToResponse(req.Tools)
		c.toolTypeMap = BuildToolTypeMap(out.Tools)
	}
	out.ToolChoice = chatToolChoiceToResponse(req.ToolChoice)

	instructions := make([]string, 0, 1)
	items := make([]*dto.ResponseInputItem, 0, len(req.Messages))
	for _, msg := range req.Messages {
		if msg == nil {
			continue
		}
		switch msg.Role {
		case enum.RoleSystem, enum.RoleDeveloper:
			if text := chatMessageText(msg.Content); text != "" {
				instructions = append(instructions, text)
			}
		case enum.RoleAssistant:
			items = append(items, chatMessageToResponseItems(msg, c.toolTypeMap, nil, false)...)
		case enum.RoleTool:
			items = append(items, chatToolMessageToResponseOutput(msg))
		default:
			items = append(items, &dto.ResponseInputItem{
				Type:    lo.ToPtr(enum.ResponseInputItemTypeMessage),
				Role:    lo.ToPtr(enum.RoleUser),
				Content: &dto.ResponseInputMessageContent{Parts: chatContentToResponseContent(msg.Content, enum.ResponseContentTypeInputText)},
			})
		}
	}
	out.Instructions = lo.ToPtr(strings.Join(instructions, constant.ResponseInstructionsJoinSeparator))
	out.Input = &dto.ResponseInput{Items: items}
	return out, nil
}

// chatMessageText 提取消息纯文本（parts 形态拼接 text 片段）。
func chatMessageText(content *dto.OpenAIMessageContent) string {
	if content == nil {
		return ""
	}
	if len(content.Parts) == 0 {
		return content.Text
	}
	var sb strings.Builder
	for _, part := range content.Parts {
		if part != nil && part.Type == enum.ContentPartTypeText {
			sb.WriteString(lo.FromPtr(part.Text))
		}
	}
	return sb.String()
}

// chatToolMessageToResponseOutput 将 Chat tool 消息转为 function_call_output。
func chatToolMessageToResponseOutput(msg *dto.OpenAIChatCompletionMessageParam) *dto.ResponseInputItem {
	item := &dto.ResponseInputItem{
		Type:   lo.ToPtr(enum.ResponseInputItemTypeFunctionCallOutput),
		CallID: msg.ToolCallID,
	}
	if msg.Content != nil && len(msg.Content.Parts) > 0 {
		item.Output = &dto.ResponseInputItemOutput{
			FunctionOutput: &dto.ResponseFunctionCallOutputContent{
				Parts: chatContentToResponseContent(msg.Content, enum.ResponseContentTypeInputText),
			},
		}
		return item
	}
	item.Output = &dto.ResponseInputItemOutput{Text: chatMessageText(msg.Content)}
	return item
}

// chatToolsToResponse 映射工具定义：function→function、custom→custom（Chat 无 namespace 概念）。
func chatToolsToResponse(tools []dto.OpenAIChatCompletionTool) []*dto.ResponseTool {
	return lo.FilterMap(tools, func(tool dto.OpenAIChatCompletionTool, _ int) (*dto.ResponseTool, bool) {
		switch {
		case tool.Function != nil:
			return &dto.ResponseTool{Function: &dto.ResponseToolFunction{
				Type:        enum.ResponseToolTypeFunction,
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  tool.Function.Parameters,
				Strict:      tool.Function.Strict,
			}}, true
		case tool.Custom != nil:
			custom := &dto.ResponseToolCustom{
				Type:        enum.ResponseToolTypeCustom,
				Name:        tool.Custom.Name,
				Description: tool.Custom.Description,
			}
			if tool.Custom.Format != nil {
				custom.Format = &dto.ResponseCustomToolFormat{Type: tool.Custom.Format.Type}
				if tool.Custom.Format.Grammar != nil {
					custom.Format.Definition = lo.ToPtr(tool.Custom.Format.Grammar.Definition)
					custom.Format.Syntax = lo.ToPtr(tool.Custom.Format.Grammar.Syntax)
				}
			}
			return &dto.ResponseTool{Custom: custom}, true
		}
		return nil, false
	})
}

// chatToolChoiceToResponse 为 responseToolChoiceToChat 的逆映射。
func chatToolChoiceToResponse(tc *dto.OpenAIChatCompletionToolChoiceParam) *dto.ResponseToolChoiceParam {
	if tc == nil {
		return nil
	}
	if tc.Named != nil && tc.Named.Function != nil {
		return &dto.ResponseToolChoiceParam{Object: &dto.ResponseToolChoiceObject{
			Type: enum.ResponseToolChoiceTypeFunction,
			Name: lo.ToPtr(tc.Named.Function.Name),
		}}
	}
	if tc.Mode != "" {
		return &dto.ResponseToolChoiceParam{Mode: tc.Mode}
	}
	return nil
}

// chatResponseFormatToResponseText 为 responseTextFormatToChat 的逆映射。
func chatResponseFormatToResponseText(format *dto.OpenAIResponseFormat) *dto.ResponseTextFormat {
	if format == nil {
		return nil
	}
	out := &dto.ResponseTextFormat{Type: format.Type}
	if format.JSONSchema != nil {
		out.Name = lo.ToPtr(format.JSONSchema.Name)
		out.Description = format.JSONSchema.Description
		out.Schema = format.JSONSchema.Schema
		out.Strict = format.JSONSchema.Strict
	}
	return out
}
