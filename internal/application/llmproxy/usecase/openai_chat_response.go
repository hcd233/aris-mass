package usecase

import (
	"cmp"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/bytedance/sonic"
	"github.com/samber/lo"

	"github.com/hcd233/aris-mass/internal/application/llmproxy/converter"
	"github.com/hcd233/aris-mass/internal/application/llmproxy/port"
	proxyutil "github.com/hcd233/aris-mass/internal/application/llmproxy/util"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/common/model"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy/aggregate"
	"github.com/hcd233/aris-mass/internal/dto"
	"github.com/hcd233/aris-mass/internal/logger"
)

var unsupportedChatChoiceCountBody = lo.Must1(sonic.Marshal(&dto.OpenAIErrorResponse{
	Error: &dto.OpenAIError{
		Message: constant.OpenAIChatChoiceCountMessage,
		Type:    constant.OpenAIInvalidRequestErrorType,
		Param:   lo.ToPtr(constant.OpenAIChatChoiceCountParam),
		Code:    constant.OpenAIUnsupportedValueCode,
	},
}))

func unsupportedChatChoicesError() *port.ProxyError {
	return &port.ProxyError{
		StatusCode: http.StatusBadRequest,
		Headers:    map[string]string{constant.HTTPHeaderContentType: constant.HTTPContentTypeJSON},
		Body:       unsupportedChatChoiceCountBody,
		Protocol:   enum.ProtocolKindOpenAI,
	}
}

// forwardChatViaResponse Chat Completions 客户端 → Responses 上游（CompatRouteViaOpenAIResponse）。
// 上游统一走 SSE（Codex 订阅强制 stream=true），客户端非流式由 consumer 聚合为完整响应。
func (u *openAIUseCase) forwardChatViaResponse(ctx context.Context, req *dto.OpenAIChatCompletionRequest, m *aggregate.Model, ep *aggregate.Endpoint) (port.Result, error) {
	conv := &converter.ResponseProtocolConverter{}
	responseReq, convErr := conv.ToResponseRequest(req.Body)
	if convErr != nil {
		logger.WithCtx(ctx).Error("[OpenAIUseCase] Failed to convert chat request to response", logger.ErrorFields(convErr)...)
		return nil, proxyutil.SendOpenAIModelNotFoundError(req.Body.Model)
	}
	upstream, err := u.buildTransportEndpoint(ctx, m, ep, false)
	if err != nil {
		return nil, err
	}
	body := proxyutil.MarshalOpenAIResponseBodyForModel(responseReq, upstream.Model)

	startTime := time.Now()
	stream, err := u.openAIProxy.OpenCreateResponseStream(ctx, upstream, body)
	if err != nil {
		totalMs := time.Since(startTime).Milliseconds()
		auditFailureWithProviders(ctx, m, u.taskSubmitter, u.tokenMetrics, req.Body.Model, ep.Name(), enum.ProtocolOpenAIResponse, enum.ProtocolOpenAIChatCompletion, totalMs, err)
		return nil, ProxyErrorFromUpstream(err, enum.ProtocolKindOpenAI, openAIInternalErrorBody)
	}

	consumer := &chatViaResponseConsumer{
		u:        u,
		ctx:      ctx,
		req:      req,
		m:        m,
		endpoint: ep.Name(),
		stream:   stream,
		timer:    newStreamTimer(),
		conv:     converter.NewResponseToChatStreamConverter(req.Body.Model),
		agg:      proxyutil.NewChatCompletionStreamAggregator(),
	}
	if lo.FromPtr(req.Body.Stream) {
		return &port.StreamResult{
			Protocol: enum.ProtocolKindOpenAI,
			Open: func() (port.Stream, error) {
				return &chatViaResponseStream{consumer}, nil
			},
		}, nil
	}
	return consumer.unary(ctx)
}

// chatViaResponseConsumer 消费 Responses 上游 SSE：事件→Chat chunk→聚合，
// 流式与非流式共用同一转换与聚合路径（非流式只是不写 chunk 给客户端）。
type chatViaResponseConsumer struct {
	u        *openAIUseCase
	ctx      context.Context
	req      *dto.OpenAIChatCompletionRequest
	m        *aggregate.Model
	endpoint string
	stream   io.ReadCloser
	timer    *streamTimer
	conv     *converter.ResponseToChatStreamConverter
	agg      *proxyutil.ChatCompletionStreamAggregator
}

func (s *chatViaResponseConsumer) consume(ctx context.Context, onChunk func(*dto.OpenAIChatCompletionChunk) error) error {
	err := s.u.openAIProxy.ReadCreateResponseStream(ctx, s.stream, func(event string, data []byte) error {
		if proxyutil.IsResponseAPIDeltaEvent(event) {
			s.timer.markFirstToken()
		}
		chunks, convErr := s.conv.HandleEvent(event, data)
		if convErr != nil {
			return convErr
		}
		if rsp := s.conv.FinalResponse(); rsp != nil && rsp.Status == enum.ResponseStatusFailed {
			return responseFailedUpstreamError(rsp)
		}
		for _, chunk := range chunks {
			s.agg.Add(chunk)
			if onChunk == nil {
				continue
			}
			if writeErr := onChunk(chunk); writeErr != nil {
				return writeErr
			}
		}
		return nil
	})
	s.timer.finish()
	if err != nil {
		return err
	}
	if s.conv.FinalResponse() == nil {
		return &model.UpstreamConnectionError{Cause: cmp.Or(ctx.Err(), io.ErrUnexpectedEOF)}
	}
	return nil
}

func (s *chatViaResponseConsumer) completion() *dto.OpenAIChatCompletion {
	completion := s.agg.Completion()
	if completion == nil {
		return nil
	}
	completion.Model = s.req.Body.Model
	return completion
}

func (s *chatViaResponseConsumer) record(err error, successStatus bool) {
	s.u.storeOpenAIChatFromCompletion(s.ctx, s.req, s.completion(), err, s.m.ModelID())
	rsp := s.conv.FinalResponse()
	recordModelCall(s.ctx, s.u.taskSubmitter, s.u.tokenMetrics, callOutcome{
		model:               s.m,
		endpoint:            s.endpoint,
		upstreamProtocol:    enum.ProtocolOpenAIResponse,
		apiProtocol:         enum.ProtocolOpenAIChatCompletion,
		firstTokenLatencyMs: s.timer.firstLatencyMs,
		streamDurationMs:    s.timer.durationMs,
		usage:               responseTokenUsage{rsp},
		successStatus:       successStatus,
		err:                 err,
		responseStatus:      rsp,
	})
}

// unary 聚合上游 SSE 为完整 ChatCompletion（客户端非流式）。
func (s *chatViaResponseConsumer) unary(ctx context.Context) (port.Result, error) {
	if err := s.consume(ctx, nil); err != nil {
		s.record(err, false)
		return nil, ProxyErrorFromUpstream(err, enum.ProtocolKindOpenAI, openAIInternalErrorBody)
	}
	completion := s.completion()
	// aggregator 继承的是 chunk 的 object，非流式响应必须归一化。
	completion.Object = enum.CompletionObjectChatCompletion
	bodyBytes := lo.Must1(sonic.Marshal(completion))
	s.record(nil, true)

	headers := buildPassthroughHeaders(ctx)
	headers[constant.HTTPHeaderContentType] = constant.HTTPContentTypeJSON
	return &port.JSONResult{
		StatusCode: http.StatusOK,
		Headers:    headers,
		Body:       bodyBytes,
		Protocol:   enum.ProtocolKindOpenAI,
	}, nil
}

// chatViaResponseStream 实现 port.Stream，向客户端写出 Chat chunk 序列。
type chatViaResponseStream struct {
	*chatViaResponseConsumer
}

// chatChunkWithUsage 保留 include_usage 所需的 usage 字段，包括普通 chunk 的 null 值。
type chatChunkWithUsage struct {
	*dto.OpenAIChatCompletionChunk
	Usage *dto.OpenAICompletionUsage `json:"usage"`
}

func (s *chatViaResponseStream) Read(ctx context.Context, sink port.EventSink) error {
	options := s.req.Body.StreamOptions
	includeUsage := options != nil && lo.FromPtr(options.IncludeUsage)
	err := s.consume(ctx, func(chunk *dto.OpenAIChatCompletionChunk) error {
		if !includeUsage && chunk.Usage != nil {
			return nil
		}

		var (
			chunkData  []byte
			marshalErr error
		)
		if includeUsage {
			chunkData, marshalErr = sonic.Marshal(chatChunkWithUsage{
				OpenAIChatCompletionChunk: chunk,
				Usage:                     chunk.Usage,
			})
		} else {
			chunkData, marshalErr = sonic.Marshal(chunk)
		}
		if marshalErr != nil {
			logger.WithCtx(ctx).Error("[OpenAIUseCase] Failed to marshal chunk", logger.ErrorFields(marshalErr)...)
			return marshalErr
		}
		return sink.WriteEvent("", chunkData)
	})
	if err != nil {
		proxyutil.WriteUpstreamSSEError(ctx, sink, err, enum.ProtocolKindOpenAI)
	} else {
		_ = sink.WriteEvent("", []byte(constant.SSEDoneSignal)) //nolint:errcheck // best-effort write
	}
	s.record(err, false)
	return err
}

// Close 兜底关闭上游 body：Read 内部已 defer Close，重复 Close 由 transport 包装保证安全。
func (s *chatViaResponseStream) Close() error {
	return s.stream.Close()
}

// responseFailedUpstreamError 将 Responses 流中的失败转换为上游错误。
func responseFailedUpstreamError(rsp *dto.OpenAICreateResponseRsp) *model.UpstreamError {
	message := constant.OpenAIInternalErrorMessage
	code := constant.OpenAIInternalErrorCode
	if rsp.Error != nil {
		message = cmp.Or(rsp.Error.Message, message)
		code = cmp.Or(rsp.Error.Code, code)
	}
	body := lo.Must1(sonic.Marshal(&dto.OpenAIErrorResponse{
		Error: &dto.OpenAIError{Message: message, Type: constant.OpenAIInternalErrorType, Code: code},
	}))
	return &model.UpstreamError{StatusCode: http.StatusBadGateway, Body: string(body)}
}
