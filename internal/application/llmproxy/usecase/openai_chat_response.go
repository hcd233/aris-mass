package usecase

import (
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
	return err
}

// streamError 汇总读取错误与 in-band response.failed（HTTP 200 但终态失败）。
func (s *chatViaResponseConsumer) streamError(readErr error) error {
	if readErr != nil {
		return readErr
	}
	rsp := s.conv.FinalResponse()
	if rsp == nil || rsp.Status != enum.ResponseStatusFailed {
		return nil
	}
	return responseFailedUpstreamError(rsp)
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
	readErr := s.consume(ctx, nil)
	if err := s.streamError(readErr); err != nil {
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

func (s *chatViaResponseStream) Read(ctx context.Context, sink port.EventSink) error {
	readErr := s.consume(ctx, func(chunk *dto.OpenAIChatCompletionChunk) error {
		chunkData, marshalErr := sonic.Marshal(chunk)
		if marshalErr != nil {
			logger.WithCtx(ctx).Error("[OpenAIUseCase] Failed to marshal chunk", logger.ErrorFields(marshalErr)...)
			return marshalErr
		}
		return sink.WriteEvent("", chunkData)
	})
	err := s.streamError(readErr)
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

// responseFailedUpstreamError 将 response.failed 终态转换为上游错误（SSE 错误帧/非流式 502）。
func responseFailedUpstreamError(rsp *dto.OpenAICreateResponseRsp) *model.UpstreamError {
	message := constant.OpenAIInternalErrorMessage
	code := constant.OpenAIInternalErrorCode
	if rsp.Error != nil {
		if rsp.Error.Message != "" {
			message = rsp.Error.Message
		}
		if rsp.Error.Code != "" {
			code = rsp.Error.Code
		}
	}
	body := lo.Must1(sonic.Marshal(&dto.OpenAIErrorResponse{
		Error: &dto.OpenAIError{Message: message, Type: constant.OpenAIInternalErrorType, Code: code},
	}))
	return &model.UpstreamError{StatusCode: http.StatusBadGateway, Body: string(body)}
}
