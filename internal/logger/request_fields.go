package logger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/ierr"
	"github.com/hcd233/aris-mass/internal/common/model"
	"go.uber.org/zap"
)

// Identifier accepts a bounded machine identifier or parameter path.
func Identifier(value string) string {
	if len(value) > constant.LogIdentifierMaxBytes {
		return ""
	}
	for i := range len(value) {
		c := value[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') &&
			(c < '0' || c > '9') && !strings.ContainsRune(constant.LogIdentifierPunctuation, rune(c)) {
			return ""
		}
	}
	return value
}

// IsJSONContentType accepts application/json and structured +json media types.
func IsJSONContentType(contentType string) bool {
	mediaType, _, _ := strings.Cut(contentType, ";")
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return mediaType == constant.HTTPContentTypeJSON || strings.HasSuffix(mediaType, constant.HTTPMediaTypeJSONSuffix)
}

type requestParams struct {
	Model               string   `json:"model,omitempty"`
	Stream              *bool    `json:"stream,omitempty"`
	Temperature         *float64 `json:"temperature,omitempty"`
	TopP                *float64 `json:"topP,omitempty"`
	TopK                *int     `json:"topK,omitempty"`
	MaxTokens           *int     `json:"maxTokens,omitempty"`
	MaxCompletionTokens *int     `json:"maxCompletionTokens,omitempty"`
	MaxOutputTokens     *int     `json:"maxOutputTokens,omitempty"`
	ReasoningEffort     string   `json:"reasoningEffort,omitempty"`
	ThinkingType        string   `json:"thinkingType,omitempty"`
	ThinkingBudget      *int     `json:"thinkingBudgetTokens,omitempty"`
	MessageCount        int      `json:"messageCount"`
	InputCount          int      `json:"inputCount"`
	ToolCount           int      `json:"toolCount"`
}

type requestBodyFields struct {
	Model               string                   `json:"model"`
	Stream              *bool                    `json:"stream"`
	Temperature         *float64                 `json:"temperature"`
	TopP                *float64                 `json:"top_p"`
	TopK                *int                     `json:"top_k"`
	MaxTokens           *int                     `json:"max_tokens"`
	MaxCompletionTokens *int                     `json:"max_completion_tokens"`
	MaxOutputTokens     *int                     `json:"max_output_tokens"`
	ReasoningEffort     string                   `json:"reasoning_effort"`
	Input               sonic.NoCopyRawMessage   `json:"input"`
	Messages            []sonic.NoCopyRawMessage `json:"messages"`
	Tools               []sonic.NoCopyRawMessage `json:"tools"`
	Reasoning           struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`
	Thinking struct {
		Type   string `json:"type"`
		Budget *int   `json:"budget_tokens"`
	} `json:"thinking"`
}

// RequestFields describes the size and selected parameters of a buffered JSON request.
func RequestFields(body []byte, inspectJSON bool) []zap.Field {
	fields := []zap.Field{zap.Int("requestBytes", len(body))}
	switch {
	case len(body) == 0:
		return fields
	case len(body) > constant.LogRequestInspectMaxBytes:
		return append(fields, zap.String("requestSummaryStatus", "size_limit"))
	case !inspectJSON:
		return append(fields, zap.String("requestSummaryStatus", "unsupported_content"))
	}

	var raw requestBodyFields
	if body = bytes.TrimSpace(body); len(body) == 0 || body[0] != '{' || sonic.Unmarshal(body, &raw) != nil {
		return append(fields, zap.String("requestSummaryStatus", "invalid_json"))
	}

	inputCount := 0
	if len(raw.Input) > 0 {
		switch raw.Input[0] {
		case '"':
			inputCount = 1
		case '[':
			var input []sonic.NoCopyRawMessage
			if sonic.Unmarshal(raw.Input, &input) == nil {
				inputCount = len(input)
			}
		}
	}

	effort := raw.Reasoning.Effort
	if effort == "" {
		effort = raw.ReasoningEffort
	}
	return append(fields, zap.Any("requestParams", requestParams{
		Model: Identifier(raw.Model), Stream: raw.Stream, Temperature: raw.Temperature, TopP: raw.TopP, TopK: raw.TopK,
		MaxTokens: raw.MaxTokens, MaxCompletionTokens: raw.MaxCompletionTokens, MaxOutputTokens: raw.MaxOutputTokens,
		ReasoningEffort: Identifier(effort), ThinkingType: Identifier(raw.Thinking.Type), ThinkingBudget: raw.Thinking.Budget,
		MessageCount: len(raw.Messages), InputCount: inputCount, ToolCount: len(raw.Tools),
	}))
}

// ErrorFields classifies errors without formatting messages that may contain input or credentials.
func ErrorFields(err error) []zap.Field {
	if err == nil {
		return nil
	}
	kind := constant.LogErrorKindInternal
	var netErr net.Error
	var upstreamErr *model.UpstreamError
	var circuitErr *model.CircuitOpenError
	var bulkheadErr *model.BulkheadFullError
	var oauthErr *model.OAuthCredentialError
	switch {
	case errors.Is(err, context.Canceled):
		kind = constant.LogErrorKindCanceled
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		kind = constant.LogErrorKindTimeout
	case errors.Is(err, io.ErrUnexpectedEOF):
		kind = constant.LogErrorKindUnexpectedEOF
	case errors.As(err, &netErr):
		kind = constant.LogErrorKindNetwork
	case errors.As(err, &upstreamErr):
		kind = constant.LogErrorKindUpstreamHTTP
	case errors.As(err, &circuitErr):
		kind = constant.LogErrorKindCircuitOpen
	case errors.As(err, &bulkheadErr):
		kind = constant.LogErrorKindBulkheadFull
	case errors.As(err, &oauthErr):
		kind = constant.LogErrorKindOAuthCredential
	}

	fields := []zap.Field{zap.String("errorKind", kind), zap.String("errorType", fmt.Sprintf("%T", err))}
	var internalErr *ierr.InternalError
	if errors.As(err, &internalErr) {
		fields = append(fields, zap.Int("internalErrorCode", internalErr.BizError().Code))
	}
	var sqlErr interface{ SQLState() string }
	if errors.As(err, &sqlErr) {
		fields = append(fields, zap.String("sqlState", Identifier(sqlErr.SQLState())))
	}
	return fields
}
