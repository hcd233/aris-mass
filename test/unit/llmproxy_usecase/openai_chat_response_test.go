package llmproxy_usecase

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/samber/lo"

	"github.com/hcd233/aris-mass/internal/application/llmproxy/port"
	"github.com/hcd233/aris-mass/internal/application/llmproxy/usecase"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/common/model"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy/service"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy/vo"
	"github.com/hcd233/aris-mass/internal/dto"
	"github.com/hcd233/aris-mass/internal/dto/schema"
)

// chatViaResponseMock 覆写 Responses 流两个方法，注入脚本化事件并捕获上游请求体。
type chatViaResponseMock struct {
	mockOpenAIProxy
	events   [][2]string
	lastBody []byte
}

func (m *chatViaResponseMock) OpenCreateResponseStream(_ context.Context, _ vo.UpstreamEndpoint, body []byte) (io.ReadCloser, error) {
	m.lastBody = append([]byte(nil), body...)
	if m.openResponseStreamErr != nil {
		return nil, m.openResponseStreamErr
	}
	return io.NopCloser(strings.NewReader("")), nil
}

func (m *chatViaResponseMock) ReadCreateResponseStream(_ context.Context, _ io.ReadCloser, onEvent func(string, []byte) error) error {
	for _, ev := range m.events {
		if err := onEvent(ev[0], []byte(ev[1])); err != nil {
			return err
		}
	}
	return nil
}

type capturingTaskSubmitter struct {
	auditTasks []*dto.ModelCallAuditTask
}

func (s *capturingTaskSubmitter) SubmitModelCallAuditTask(task *dto.ModelCallAuditTask) error {
	s.auditTasks = append(s.auditTasks, task)
	return nil
}

func (s *capturingTaskSubmitter) SubmitMessageStoreTask(_ *dto.MessageStoreTask) error { return nil }

var _ usecase.TaskSubmitter = (*capturingTaskSubmitter)(nil)

func chatViaResponseToolEvents() [][2]string {
	return [][2]string{
		{enum.ResponseStreamEventOutputTextDelta, `{"item_id":"msg_1","delta":"ok"}`},
		{enum.ResponseStreamEventOutputItemAdded, `{"output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather"}}`},
		{enum.ResponseStreamEventFunctionCallArgumentsDelta, `{"item_id":"fc_1","delta":"{\"city\":\"SZ\"}"}`},
		{enum.ResponseStreamEventCompleted, `{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`},
	}
}

func chatViaResponseRequest(stream bool) *dto.OpenAIChatCompletionRequest {
	return &dto.OpenAIChatCompletionRequest{Body: &dto.OpenAIChatCompletionReq{
		Model:  "test-alias",
		Stream: lo.ToPtr(stream),
		Messages: []*dto.OpenAIChatCompletionMessageParam{
			{Role: enum.RoleSystem, Content: &dto.OpenAIMessageContent{Text: "sys"}},
			{Role: enum.RoleUser, Content: &dto.OpenAIMessageContent{Text: "Hello"}},
		},
		Tools: []dto.OpenAIChatCompletionTool{
			{Type: enum.ToolTypeFunction, Function: &dto.OpenAIFunctionDefinition{Name: "get_weather", Parameters: &schema.JSONSchemaProperty{}}},
		},
	}}
}

// 非流式 Chat 命中 Responses-only 端点：上游收到 Responses 流式请求，
// 客户端拿到聚合后的完整 ChatCompletion（object 归一化、工具调用与 usage 齐全）。
func TestForwardChatViaResponse_Unary(t *testing.T) {
	t.Parallel()
	proxy := &chatViaResponseMock{events: chatViaResponseToolEvents()}
	submitter := &capturingTaskSubmitter{}
	resolver := &mockResolver{resolveEndpoint: buildCompatEndpoint("response-only", false, true, false), resolveModel: buildTestModel()}
	uc := usecase.NewOpenAIUseCase(resolver, &mockListModels{}, proxy, &mockAnthropicProxyForOpenAI{}, submitter, nil, nil, nil, nil)

	result, err := uc.CreateChatCompletion(context.Background(), chatViaResponseRequest(false))
	if err != nil {
		t.Fatalf("CreateChatCompletion() error: %v", err)
	}
	jsonResult, ok := result.(*port.JSONResult)
	if !ok {
		t.Fatalf("result = %T, want *port.JSONResult", result)
	}

	var upstreamReq dto.OpenAICreateResponseReq
	if err := sonic.Unmarshal(proxy.lastBody, &upstreamReq); err != nil {
		t.Fatalf("upstream body unmarshal error: %v", err)
	}
	if !lo.FromPtr(upstreamReq.Stream) {
		t.Fatal("upstream stream = false, want true")
	}
	if lo.FromPtr(upstreamReq.Model) != "test-model" {
		t.Fatalf("upstream model = %q, want 上游真实模型名", lo.FromPtr(upstreamReq.Model))
	}
	if lo.FromPtr(upstreamReq.Instructions) != "sys" {
		t.Fatalf("upstream instructions = %q", lo.FromPtr(upstreamReq.Instructions))
	}
	if upstreamReq.Input == nil || len(upstreamReq.Input.Items) != 1 || lo.FromPtr(upstreamReq.Input.Items[0].Role) != enum.RoleUser {
		t.Fatalf("upstream input = %+v", upstreamReq.Input)
	}
	if len(upstreamReq.Tools) != 1 || upstreamReq.Tools[0].Function == nil || upstreamReq.Tools[0].Function.Name != "get_weather" {
		t.Fatalf("upstream tools = %+v", upstreamReq.Tools)
	}

	var completion dto.OpenAIChatCompletion
	if err := sonic.Unmarshal(jsonResult.Body, &completion); err != nil {
		t.Fatalf("completion unmarshal error: %v", err)
	}
	if completion.Object != enum.CompletionObjectChatCompletion {
		t.Fatalf("object = %q, want chat.completion（不得继承 chunk object）", completion.Object)
	}
	if completion.Model != "test-alias" {
		t.Fatalf("model = %q, want 客户端别名", completion.Model)
	}
	choice := completion.Choices[0]
	if choice.Message.Content == nil || choice.Message.Content.Text != "ok" {
		t.Fatalf("message content = %+v", choice.Message.Content)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %+v", choice.Message.ToolCalls)
	}
	tc := choice.Message.ToolCalls[0]
	if lo.FromPtr(tc.ID) != "call_1" || tc.Function.Name != "get_weather" || tc.Function.Arguments != `{"city":"SZ"}` {
		t.Fatalf("tool_call = %+v", tc)
	}
	if choice.FinishReason != enum.FinishReasonToolCalls {
		t.Fatalf("finish_reason = %v", choice.FinishReason)
	}
	if completion.Usage == nil || completion.Usage.PromptTokens != 10 || completion.Usage.CompletionTokens != 5 {
		t.Fatalf("usage = %+v", completion.Usage)
	}

	if len(submitter.auditTasks) != 1 {
		t.Fatalf("audit tasks = %d, want 1", len(submitter.auditTasks))
	}
	audit := submitter.auditTasks[0]
	if audit.UpstreamProtocol != enum.ProtocolOpenAIResponse || audit.APIProtocol != enum.ProtocolOpenAIChatCompletion {
		t.Fatalf("audit protocols = %s/%s", audit.UpstreamProtocol, audit.APIProtocol)
	}
	if audit.OutputTokens != 5 {
		t.Fatalf("audit output tokens = %d", audit.OutputTokens)
	}
}

type collectEventSink struct {
	events [][2]string
}

func (s *collectEventSink) WriteEvent(event string, data []byte) error {
	s.events = append(s.events, [2]string{event, string(data)})
	return nil
}

func TestForwardChatViaResponse_Stream(t *testing.T) {
	t.Parallel()
	proxy := &chatViaResponseMock{events: chatViaResponseToolEvents()}
	resolver := &mockResolver{resolveEndpoint: buildCompatEndpoint("response-only", false, true, false), resolveModel: buildTestModel()}
	uc := usecase.NewOpenAIUseCase(resolver, &mockListModels{}, proxy, &mockAnthropicProxyForOpenAI{}, &mockTaskSubmitter{}, nil, nil, nil, nil)

	result, err := uc.CreateChatCompletion(context.Background(), chatViaResponseRequest(true))
	if err != nil {
		t.Fatalf("CreateChatCompletion() error: %v", err)
	}
	streamResult, ok := result.(*port.StreamResult)
	if !ok {
		t.Fatalf("result = %T, want *port.StreamResult", result)
	}
	stream, err := streamResult.Open()
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer func() { _ = stream.Close() }() //nolint:errcheck // test cleanup

	sink := &collectEventSink{}
	if err := stream.Read(context.Background(), sink); err != nil {
		t.Fatalf("Read() error: %v", err)
	}
	if len(sink.events) != 5 {
		t.Fatalf("events = %d, want 4 chunk + [DONE]", len(sink.events))
	}
	var chunks []*dto.OpenAIChatCompletionChunk
	for _, ev := range sink.events[:4] {
		if strings.Contains(ev[1], `"usage"`) {
			t.Errorf("chunk includes usage without an opt-in: %s", ev[1])
		}
		chunk := &dto.OpenAIChatCompletionChunk{}
		if err := sonic.UnmarshalString(ev[1], chunk); err != nil {
			t.Fatalf("chunk unmarshal error: %v", err)
		}
		chunks = append(chunks, chunk)
	}
	if chunks[0].Choices[0].Delta.Role != enum.RoleAssistant || lo.FromPtr(chunks[0].Choices[0].Delta.Content) != "ok" {
		t.Fatalf("first chunk = %+v", chunks[0].Choices[0].Delta)
	}
	if lo.FromPtr(chunks[1].Choices[0].Delta.ToolCalls[0].ID) != "call_1" {
		t.Fatalf("tool call chunk = %+v", chunks[1].Choices[0].Delta.ToolCalls[0])
	}
	if lo.FromPtr(chunks[3].Choices[0].FinishReason) != enum.FinishReasonToolCalls {
		t.Fatalf("final chunk = %+v", chunks[3].Choices[0])
	}
	if sink.events[4][1] != "[DONE]" {
		t.Fatalf("last event = %q, want [DONE]", sink.events[4][1])
	}
}

func TestForwardChatViaResponse_StreamUsage(t *testing.T) {
	t.Parallel()
	proxy := &chatViaResponseMock{events: chatViaResponseToolEvents()}
	submitter := &capturingTaskSubmitter{}
	resolver := &mockResolver{resolveEndpoint: buildCompatEndpoint("response-only", false, true, false), resolveModel: buildTestModel()}
	uc := usecase.NewOpenAIUseCase(resolver, &mockListModels{}, proxy, &mockAnthropicProxyForOpenAI{}, submitter, nil, nil, nil, nil)
	req := chatViaResponseRequest(true)
	req.Body.StreamOptions = &dto.OpenAIChatCompletionStreamOptions{IncludeUsage: lo.ToPtr(true)}

	result, err := uc.CreateChatCompletion(t.Context(), req)
	if err != nil {
		t.Fatalf("CreateChatCompletion() error: %v", err)
	}
	stream, err := result.(*port.StreamResult).Open()
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() }) //nolint:errcheck // test cleanup
	sink := &collectEventSink{}
	if err := stream.Read(t.Context(), sink); err != nil {
		t.Fatalf("Read() error: %v", err)
	}
	if len(sink.events) != 6 || sink.events[5][1] != "[DONE]" {
		t.Fatalf("events = %+v, want 4 choice chunks, a usage chunk, then DONE", sink.events)
	}
	for _, event := range sink.events[:4] {
		if !strings.Contains(event[1], `"usage":null`) {
			t.Errorf("choice chunk should report null usage: %s", event[1])
		}
	}
	var usageChunk, finishChunk dto.OpenAIChatCompletionChunk
	if err := sonic.UnmarshalString(sink.events[4][1], &usageChunk); err != nil {
		t.Fatal(err)
	}
	if err := sonic.UnmarshalString(sink.events[3][1], &finishChunk); err != nil {
		t.Fatal(err)
	}
	if usageChunk.Choices == nil || len(usageChunk.Choices) != 0 || usageChunk.Usage == nil || usageChunk.Usage.TotalTokens != 15 {
		t.Fatalf("usage chunk = %s, want empty choices and total usage of 15", sink.events[4][1])
	}
	if usageChunk.ID != finishChunk.ID || usageChunk.Created != finishChunk.Created {
		t.Fatalf("usage and finish chunks have different completion metadata: %+v, %+v", usageChunk, finishChunk)
	}
	if len(submitter.auditTasks) != 1 || submitter.auditTasks[0].OutputTokens != 5 {
		t.Fatalf("audit tasks = %+v, want one call with 5 output tokens", submitter.auditTasks)
	}
}

func TestForwardChatViaResponse_AbnormalStreamEnd(t *testing.T) {
	t.Parallel()
	partial := chatViaResponseToolEvents()[:1]
	upstreamError := [][2]string{{"error", `{"type":"error","code":"overloaded","message":"upstream overloaded"}`}}
	cases := []struct {
		name     string
		stream   bool
		events   [][2]string
		wantCode string
	}{
		{name: "unary unexpected eof", events: partial},
		{name: "stream unexpected eof", stream: true, events: partial},
		{name: "unary upstream error", events: upstreamError, wantCode: "overloaded"},
		{name: "stream upstream error", stream: true, events: upstreamError, wantCode: "overloaded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proxy := &chatViaResponseMock{events: tc.events}
			submitter := &capturingTaskSubmitter{}
			resolver := &mockResolver{resolveEndpoint: buildCompatEndpoint("response-only", false, true, false), resolveModel: buildTestModel()}
			uc := usecase.NewOpenAIUseCase(resolver, &mockListModels{}, proxy, &mockAnthropicProxyForOpenAI{}, submitter, nil, nil, nil, nil)
			result, err := uc.CreateChatCompletion(t.Context(), chatViaResponseRequest(tc.stream))
			var errorBody []byte
			if tc.stream {
				if err != nil {
					t.Fatalf("CreateChatCompletion() error: %v", err)
				}
				stream, openErr := result.(*port.StreamResult).Open()
				if openErr != nil {
					t.Fatalf("Open() error: %v", openErr)
				}
				t.Cleanup(func() { _ = stream.Close() }) //nolint:errcheck // test cleanup
				sink := &collectEventSink{}
				if readErr := stream.Read(t.Context(), sink); readErr == nil {
					t.Fatal("Read() succeeded without a successful terminal response")
				}
				for _, event := range sink.events {
					if event[1] == "[DONE]" {
						t.Fatal("abnormal stream sent DONE")
					}
				}
				if len(sink.events) == 0 {
					t.Fatal("abnormal stream sent no error frame")
				}
				errorBody = []byte(sink.events[len(sink.events)-1][1])
			} else {
				var proxyErr *port.ProxyError
				if !errors.As(err, &proxyErr) || proxyErr.StatusCode != http.StatusBadGateway || result != nil {
					t.Fatalf("result = %T, err = %v, want upstream 502", result, err)
				}
				errorBody = proxyErr.Body
			}
			var openAIErr dto.OpenAIErrorResponse
			if err := sonic.Unmarshal(errorBody, &openAIErr); err != nil || openAIErr.Error == nil {
				t.Fatalf("want an OpenAI error envelope, got %s (%v)", errorBody, err)
			}
			if tc.wantCode != "" && openAIErr.Error.Code != tc.wantCode {
				t.Fatalf("error code = %q, want %q", openAIErr.Error.Code, tc.wantCode)
			}
			if len(submitter.auditTasks) != 1 || submitter.auditTasks[0].UpstreamStatusCode == http.StatusOK {
				t.Fatalf("audit tasks = %+v, want one failed call", submitter.auditTasks)
			}
		})
	}
}

func TestCreateChatCompletion_MultipleChoicesRequiresNativeEndpoint(t *testing.T) {
	t.Parallel()
	responseOnly := service.Candidate{Endpoint: buildCompatEndpoint("response-only", false, true, false), Model: buildTestModel()}
	anthropicOnly := service.Candidate{Endpoint: buildCompatEndpoint("anthropic-only", false, false, true), Model: buildTestModel()}
	native := service.Candidate{Endpoint: buildCompatEndpoint("native", true, false, false), Model: buildTestModel()}
	cases := []struct {
		name       string
		candidates []service.Candidate
		wantNative bool
	}{
		{name: "native candidate", candidates: []service.Candidate{responseOnly, native}, wantNative: true},
		{name: "native after both conversion candidates", candidates: []service.Candidate{responseOnly, anthropicOnly, native}, wantNative: true},
		{name: "responses only", candidates: []service.Candidate{responseOnly}},
		{name: "both conversion candidates", candidates: []service.Candidate{responseOnly, anthropicOnly}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proxy := &chatViaResponseMock{events: chatViaResponseToolEvents()}
			resolver := &multiCandidateResolver{cands: tc.candidates}
			uc := usecase.NewOpenAIUseCase(resolver, &mockListModels{}, proxy, &mockAnthropicProxyForOpenAI{}, &mockTaskSubmitter{}, nil, nil, nil, nil)
			req := chatViaResponseRequest(false)
			req.Body.N = lo.ToPtr(2)
			result, err := uc.CreateChatCompletion(t.Context(), req)
			if tc.wantNative {
				if err != nil || result == nil || !proxy.chatUnaryCalled || len(proxy.lastBody) != 0 {
					t.Fatalf("result = %T, err = %v, want native Chat result", result, err)
				}
				return
			}
			var proxyErr *port.ProxyError
			if !errors.As(err, &proxyErr) || proxyErr.StatusCode != http.StatusBadRequest {
				t.Fatalf("err = %v, want invalid request 400", err)
			}
			var openAIErr dto.OpenAIErrorResponse
			if err := sonic.Unmarshal(proxyErr.Body, &openAIErr); err != nil || openAIErr.Error == nil {
				t.Fatalf("want an OpenAI error envelope, got %s (%v)", proxyErr.Body, err)
			}
			if lo.FromPtr(openAIErr.Error.Param) != "n" || len(proxy.lastBody) != 0 {
				t.Fatalf("error = %+v, want rejected parameter n before upstream", openAIErr.Error)
			}
		})
	}
}

type chatViaResponseUsageReporter int64

func (r *chatViaResponseUsageReporter) Report(_ context.Context, tokens int64) {
	*r += chatViaResponseUsageReporter(tokens)
}

func TestForwardChatViaResponse_FallbackAuditsFinalAttempt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		events     [][2]string
		wantTokens int64
	}{
		{name: "truncated response", events: chatViaResponseToolEvents()[:1], wantTokens: 2},
		{
			name: "billed upstream failure",
			events: [][2]string{{
				enum.ResponseStreamEventFailed,
				`{"response":{"status":"failed","error":{"code":"server_error","message":"unavailable"},"usage":{"input_tokens":3,"output_tokens":4}}}`,
			}},
			wantTokens: 9,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proxy := &chatViaResponseMock{events: tc.events}
			submitter := &capturingTaskSubmitter{}
			resolver := &multiCandidateResolver{cands: []service.Candidate{
				{Endpoint: buildCompatEndpoint("response-only", false, true, false), Model: buildTestModel()},
				{Endpoint: buildCompatEndpoint("native", true, false, false), Model: buildTestModel()},
			}}
			uc := usecase.NewOpenAIUseCase(resolver, &mockListModels{}, proxy, &mockAnthropicProxyForOpenAI{}, submitter, nil, nil, nil, nil)
			var reporter chatViaResponseUsageReporter
			ctx := context.WithValue(t.Context(), constant.CtxKeyTokenUsageReporter, &reporter)

			result, err := uc.CreateChatCompletion(ctx, chatViaResponseRequest(false))
			if err != nil || result == nil || !proxy.chatUnaryCalled {
				t.Fatalf("result = %T, err = %v, want successful native fallback", result, err)
			}
			if len(submitter.auditTasks) != 1 || submitter.auditTasks[0].Endpoint != "native" {
				t.Fatalf("audit tasks = %+v, want only the final native attempt", submitter.auditTasks)
			}
			if int64(reporter) != tc.wantTokens {
				t.Fatalf("reported tokens = %d, want %d", reporter, tc.wantTokens)
			}
		})
	}
}

// 上游打开失败：返回 ProxyError 且不进入 Read（错误在 SSE 头写出前暴露）。
func TestForwardChatViaResponse_OpenError(t *testing.T) {
	t.Parallel()
	upstreamErr := &model.UpstreamError{StatusCode: 429, Body: `{"error":{"message":"rate limited"}}`}
	proxy := &chatViaResponseMock{}
	proxy.openResponseStreamErr = upstreamErr
	resolver := &mockResolver{resolveEndpoint: buildCompatEndpoint("response-only", false, true, false), resolveModel: buildTestModel()}
	uc := usecase.NewOpenAIUseCase(resolver, &mockListModels{}, proxy, &mockAnthropicProxyForOpenAI{}, &mockTaskSubmitter{}, nil, nil, nil, nil)

	result, err := uc.CreateChatCompletion(context.Background(), chatViaResponseRequest(true))
	if result != nil {
		t.Fatalf("result = %T, want nil", result)
	}
	var proxyErr *port.ProxyError
	if !errors.As(err, &proxyErr) {
		t.Fatalf("err = %v, want *port.ProxyError", err)
	}
	if proxyErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429（透传上游状态码）", proxyErr.StatusCode)
	}
}
