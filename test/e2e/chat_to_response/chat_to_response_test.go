// Package chat_to_response 验证 Chat Completions 客户端 → Responses 上游的端到端转换。
//
// 生产同构装配：RegisterAPIRouter + APIKey 中间件 + 真实 transport/usecase/adapter
// + sqlite 仓储 + miniredis + httptest Responses 上游（SSE）+ 真实 TCP listener。
// 覆盖：非流式聚合（含工具调用）、流式 chunk 序列、多轮工具循环历史映射、
// Chat 能力端点的 native 回归（新兜底路由不改变既有选路）。
package chat_to_response

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bytedance/sonic"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/hcd233/aris-mass/internal/application/llmproxy/usecase"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/common/inflight"
	llmproxyservice "github.com/hcd233/aris-mass/internal/domain/llmproxy/service"
	"github.com/hcd233/aris-mass/internal/dto"
	"github.com/hcd233/aris-mass/internal/handler"
	dbmodel "github.com/hcd233/aris-mass/internal/infrastructure/database/model"
	"github.com/hcd233/aris-mass/internal/infrastructure/httpclient"
	"github.com/hcd233/aris-mass/internal/infrastructure/metrics"
	"github.com/hcd233/aris-mass/internal/infrastructure/repository"
	"github.com/hcd233/aris-mass/internal/infrastructure/transport"
	"github.com/hcd233/aris-mass/internal/router"
)

type (
	stubPingHandler      struct{ handler.PingHandler }
	stubTokenHandler     struct{ handler.TokenHandler }
	stubOauth2Handler    struct{ handler.Oauth2Handler }
	stubUserHandler      struct{ handler.UserHandler }
	stubDemoHandler      struct{ handler.DemoHandler }
	stubAPIKeyHandler    struct{ handler.APIKeyHandler }
	stubSessionHandler   struct{ handler.SessionHandler }
	stubEndpointHandler  struct{ handler.EndpointHandler }
	stubModelHandler     struct{ handler.ModelHandler }
	stubAuditHandler     struct{ handler.AuditHandler }
	stubCronHandler      struct{ handler.CronHandler }
	stubAnthropicHandler struct{ handler.AnthropicHandler }
	stubTriggerHandler   struct{ handler.TriggerHandler }
	stubMetricsHandler   struct{ handler.MetricsHandler }
	stubDatasetHandler   struct{ handler.DatasetHandler }
	stubClientHandler    struct{ handler.ClientHandler }
	stubUpstreamHandler  struct{ handler.UpstreamHandler }
)

// capturingSubmitter 捕获审计与存储任务（异步池的同步替身）。
type capturingSubmitter struct {
	mu         sync.Mutex
	auditTasks []*dto.ModelCallAuditTask
	storeTasks []*dto.MessageStoreTask
}

func (s *capturingSubmitter) SubmitModelCallAuditTask(task *dto.ModelCallAuditTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditTasks = append(s.auditTasks, task)
	return nil
}

func (s *capturingSubmitter) SubmitMessageStoreTask(task *dto.MessageStoreTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storeTasks = append(s.storeTasks, task)
	return nil
}

type noopTriggerChecker struct{}

func (noopTriggerChecker) Check(_ string) []uint                           { return nil }
func (noopTriggerChecker) MatchedWords(_ []uint) []string                  { return nil }
func (noopTriggerChecker) DenyIDs(_ []uint) []uint                         { return nil }
func (noopTriggerChecker) OmitIDs(_ []uint) []uint                         { return nil }
func (noopTriggerChecker) CaptureIDs(_ []uint) []uint                      { return nil }
func (noopTriggerChecker) IncrementHits(_ context.Context, _ []uint) error { return nil }

// upstreamCapture 记录上游收到的请求路径与 body。
type upstreamCapture struct {
	mu     sync.Mutex
	path   string
	body   []byte
	called int
}

func (c *upstreamCapture) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.path = r.URL.Path
	c.body, _ = io.ReadAll(r.Body)
	c.called++
}

func (c *upstreamCapture) lastBody(t *testing.T) []byte {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.called == 0 {
		t.Fatal("上游未收到请求")
	}
	return c.body
}

const testAPIKey = "sk-chat-to-response"

type fixture struct {
	baseURL   string
	client    *http.Client
	capture   *upstreamCapture
	submitter *capturingSubmitter
}

// toolSSE 是一段典型 Responses 上游流：文本增量 + 一次 function_call + 终态 usage。
const toolSSE = `event: response.output_text.delta
data: {"item_id":"msg_1","delta":"ok"}

event: response.output_item.added
data: {"output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather"}}

event: response.function_call_arguments.delta
data: {"item_id":"fc_1","delta":"{\"city\":\"SZ\"}"}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}

`

func newFixture(t *testing.T, supportChat bool, upstreamHandler http.HandlerFunc) *fixture {
	t.Helper()
	capture := &upstreamCapture{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		upstreamHandler(w, r)
	}))
	t.Cleanup(upstream.Close)

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"),
		&gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&dbmodel.User{}, &dbmodel.ProxyAPIKey{}, &dbmodel.Endpoint{}, &dbmodel.Model{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	httpclient.InitHTTPClient()
	registry := metrics.NewRegistry()
	tracker := inflight.NewTracker()
	guard := transport.NewEndpointGuard(registry)
	openAIProxy := transport.NewOpenAIProxy(tracker, guard)
	anthropicProxy := transport.NewAnthropicProxy(tracker, guard)

	endpointRepo := repository.NewEndpointRepository(db)
	modelRepo := repository.NewModelRepository(db)
	resolver := llmproxyservice.NewEndpointResolver(endpointRepo, modelRepo, false, nil)
	listModels := usecase.NewListOpenAIModels(repository.NewEndpointReadRepository(db))

	submitter := &capturingSubmitter{}
	openAIUC := usecase.NewOpenAIUseCase(resolver, listModels, openAIProxy, anthropicProxy,
		submitter, noopTriggerChecker{}, metrics.NewTokenUsageCounter(registry), nil, nil)
	sseGauge := metrics.NewSSEGauge(registry)
	openAIHandler := handler.NewOpenAIHandler(handler.OpenAIDependencies{UseCase: openAIUC, SSEGauge: sseGauge})
	playgroundHandler := handler.NewPlaygroundHandler(handler.PlaygroundDependencies{UseCase: openAIUC, SSEGauge: sseGauge})

	app := fiber.New()
	api := humafiber.New(app, huma.DefaultConfig("chat to response", "1.0"))
	router.RegisterAPIRouter(api, router.APIRouterDependencies{
		DB:                db,
		Cache:             rdb,
		PingHandler:       &stubPingHandler{},
		TokenHandler:      &stubTokenHandler{},
		Oauth2Handler:     &stubOauth2Handler{},
		UserHandler:       &stubUserHandler{},
		DemoHandler:       &stubDemoHandler{},
		APIKeyHandler:     &stubAPIKeyHandler{},
		SessionHandler:    &stubSessionHandler{},
		EndpointHandler:   &stubEndpointHandler{},
		ModelHandler:      &stubModelHandler{},
		AuditHandler:      &stubAuditHandler{},
		CronHandler:       &stubCronHandler{},
		OpenAIHandler:     openAIHandler,
		PlaygroundHandler: playgroundHandler,
		AnthropicHandler:  &stubAnthropicHandler{},
		TriggerHandler:    &stubTriggerHandler{},
		MetricsHandler:    &stubMetricsHandler{},
		DatasetHandler:    &stubDatasetHandler{},
		ClientHandler:     &stubClientHandler{},
		UpstreamHandler:   &stubUpstreamHandler{},
	})

	user := &dbmodel.User{Name: "c2r-user", GithubBindID: "gh-c2r", GoogleBindID: "gg-c2r", Permission: enum.PermissionUser}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	key := &dbmodel.ProxyAPIKey{UserID: user.ID, Name: "c2r", Key: testAPIKey}
	if err := db.Create(key).Error; err != nil {
		t.Fatalf("create api key: %v", err)
	}
	ep := &dbmodel.Endpoint{UserID: user.ID, Name: "ep-c2r", APIKey: "sk-upstream",
		OpenaiBaseURL: upstream.URL, SupportOpenAIChatCompletion: supportChat, SupportOpenAIResponse: true}
	if err := db.Create(ep).Error; err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	// support_openai_chat_completion 有 default:true，GORM 创建时 false 会被默认值覆盖，须显式回写
	if err := db.Model(&dbmodel.Endpoint{}).Where("id = ?", ep.ID).
		Update("support_openai_chat_completion", supportChat).Error; err != nil {
		t.Fatalf("update endpoint capability: %v", err)
	}
	m := &dbmodel.Model{UserID: user.ID, Alias: "c2r-alias", ModelID: "c2r-alias", UpstreamModel: "gpt-up",
		EndpointID: ep.ID, Capabilities: []enum.InputModality{enum.InputModalityText}}
	if err := db.Create(m).Error; err != nil {
		t.Fatalf("create model: %v", err)
	}

	listenConfig := net.ListenConfig{}
	ln, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = app.ShutdownWithContext(shutdownCtx)
	})

	f := &fixture{
		baseURL:   "http://" + ln.Addr().String(),
		client:    &http.Client{Timeout: 30 * time.Second},
		capture:   capture,
		submitter: submitter,
	}
	f.waitReady(t)
	return f
}

func (f *fixture) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, f.baseURL+"/api/openai/v1/models", http.NoBody)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		resp, err := f.client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		<-time.After(100 * time.Millisecond) //nolint:revive // 就绪轮询间隔
	}
	t.Fatal("server not ready in 10s")
}

func (f *fixture) postChat(t *testing.T, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		f.baseURL+"/api/openai/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("send chat request: %v", err)
	}
	return resp
}

const toolRequestBody = `{
	"model":"c2r-alias",
	"messages":[
		{"role":"system","content":"sys"},
		{"role":"user","content":"深圳天气？"}
	],
	"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],
	"stream":false
}`

// 非流式 + 工具调用：上游收 Responses 流式请求，客户端收聚合后的完整 ChatCompletion。
func TestChatToResponse_UnaryWithTools(t *testing.T) {
	t.Parallel()
	f := newFixture(t, false, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, toolSSE)
	})

	resp := f.postChat(t, toolRequestBody)
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // test cleanup
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body: %s", resp.StatusCode, respBody)
	}

	var upstreamReq dto.OpenAICreateResponseReq
	if err := sonic.Unmarshal(f.capture.lastBody(t), &upstreamReq); err != nil {
		t.Fatalf("upstream body unmarshal: %v", err)
	}
	if f.capture.path != "/responses" {
		t.Fatalf("upstream path = %q, want /responses", f.capture.path)
	}
	if upstreamReq.Stream == nil || !*upstreamReq.Stream {
		t.Fatal("upstream stream 必须为 true")
	}
	if upstreamReq.Instructions == nil || *upstreamReq.Instructions != "sys" {
		t.Fatalf("upstream instructions = %v", upstreamReq.Instructions)
	}
	if upstreamReq.Model == nil || *upstreamReq.Model != "gpt-up" {
		t.Fatalf("upstream model = %v", upstreamReq.Model)
	}
	if len(upstreamReq.Tools) != 1 || upstreamReq.Tools[0].Function == nil || upstreamReq.Tools[0].Function.Name != "get_weather" {
		t.Fatalf("upstream tools = %+v", upstreamReq.Tools)
	}

	var completion dto.OpenAIChatCompletion
	if err := sonic.Unmarshal(respBody, &completion); err != nil {
		t.Fatalf("completion unmarshal: %v", err)
	}
	if completion.Object != enum.CompletionObjectChatCompletion {
		t.Fatalf("object = %q, want chat.completion", completion.Object)
	}
	if completion.Model != "c2r-alias" {
		t.Fatalf("model = %q, want 客户端别名", completion.Model)
	}
	choice := completion.Choices[0]
	if choice.Message.Content == nil || choice.Message.Content.Text != "ok" {
		t.Fatalf("content = %+v", choice.Message.Content)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %+v", choice.Message.ToolCalls)
	}
	tc := choice.Message.ToolCalls[0]
	if tc.ID == nil || *tc.ID != "call_1" || tc.Function.Name != "get_weather" || tc.Function.Arguments != `{"city":"SZ"}` {
		t.Fatalf("tool_call = %+v", tc)
	}
	if choice.FinishReason != enum.FinishReasonToolCalls {
		t.Fatalf("finish_reason = %v", choice.FinishReason)
	}
	if completion.Usage == nil || completion.Usage.PromptTokens != 10 || completion.Usage.CompletionTokens != 5 {
		t.Fatalf("usage = %+v", completion.Usage)
	}

	if len(f.submitter.auditTasks) != 1 {
		t.Fatalf("audit tasks = %d, want 1", len(f.submitter.auditTasks))
	}
	audit := f.submitter.auditTasks[0]
	if audit.UpstreamProtocol != enum.ProtocolOpenAIResponse || audit.APIProtocol != enum.ProtocolOpenAIChatCompletion {
		t.Fatalf("audit protocols = %s/%s", audit.UpstreamProtocol, audit.APIProtocol)
	}
	if len(f.submitter.storeTasks) != 1 {
		t.Fatalf("store tasks = %d, want 1（会话落库）", len(f.submitter.storeTasks))
	}
}

// 流式：chunk 序列含 role/content/tool_calls/finish_reason，末尾 [DONE]。
func TestChatToResponse_Stream(t *testing.T) {
	t.Parallel()
	f := newFixture(t, false, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, part := range strings.SplitAfter(toolSSE, "\n\n") {
			if strings.TrimSpace(part) == "" {
				continue
			}
			_, _ = io.WriteString(w, part)
			flusher.Flush()
		}
	})

	body := strings.Replace(toolRequestBody, `"stream":false`, `"stream":true`, 1)
	resp := f.postChat(t, body)
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // test cleanup
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body: %s", resp.StatusCode, respBody)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}

	var chunks []*dto.OpenAIChatCompletionChunk
	done := false
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if !strings.HasPrefix(trimmed, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(trimmed, "data: ")
		if payload == "[DONE]" {
			done = true
			break
		}
		chunk := &dto.OpenAIChatCompletionChunk{}
		if err := sonic.UnmarshalString(payload, chunk); err != nil {
			t.Fatalf("chunk unmarshal: %v, payload: %s", err, payload)
		}
		chunks = append(chunks, chunk)
	}
	if !done {
		t.Fatal("未收到 [DONE]")
	}
	if len(chunks) != 4 {
		t.Fatalf("chunks = %d, want 4", len(chunks))
	}
	if chunks[0].Choices[0].Delta.Role != enum.RoleAssistant || chunks[0].Choices[0].Delta.Content == nil || *chunks[0].Choices[0].Delta.Content != "ok" {
		t.Fatalf("first chunk = %+v", chunks[0].Choices[0].Delta)
	}
	if chunks[1].Choices[0].Delta.ToolCalls[0].ID == nil || *chunks[1].Choices[0].Delta.ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool call chunk = %+v", chunks[1].Choices[0].Delta)
	}
	if chunks[3].Choices[0].FinishReason == nil || *chunks[3].Choices[0].FinishReason != enum.FinishReasonToolCalls {
		t.Fatalf("final chunk = %+v", chunks[3].Choices[0])
	}
}

// 多轮工具循环：assistant tool_calls + tool 结果必须映射为 function_call/function_call_output。
func TestChatToResponse_MultiTurnToolLoop(t *testing.T) {
	t.Parallel()
	f := newFixture(t, false, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `event: response.output_text.delta
data: {"item_id":"msg_1","delta":"晴天"}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_2","status":"completed","usage":{"input_tokens":20,"output_tokens":2,"total_tokens":22}}}

`)
	})

	body := `{
		"model":"c2r-alias",
		"messages":[
			{"role":"user","content":"深圳天气？"},
			{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SZ\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"sunny"}
		],
		"stream":false
	}`
	resp := f.postChat(t, body)
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // test cleanup
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body: %s", resp.StatusCode, respBody)
	}

	var upstreamReq dto.OpenAICreateResponseReq
	if err := sonic.Unmarshal(f.capture.lastBody(t), &upstreamReq); err != nil {
		t.Fatalf("upstream body unmarshal: %v", err)
	}
	if upstreamReq.Input == nil {
		t.Fatal("upstream input 为空")
	}
	var call, output *dto.ResponseInputItem
	for _, item := range upstreamReq.Input.Items {
		switch {
		case item.Type != nil && *item.Type == enum.ResponseInputItemTypeFunctionCall:
			call = item
		case item.Type != nil && *item.Type == enum.ResponseInputItemTypeFunctionCallOutput:
			output = item
		}
	}
	if call == nil || call.CallID == nil || *call.CallID != "call_1" || call.Name == nil || *call.Name != "get_weather" {
		t.Fatalf("function_call = %+v", call)
	}
	if output == nil || output.CallID == nil || *output.CallID != "call_1" {
		t.Fatalf("function_call_output = %+v", output)
	}
	if output.Output == nil || output.Output.Text != "sunny" {
		t.Fatalf("function_call_output.output = %+v", output.Output)
	}
}

// 回归：具备 Chat 能力的端点仍走 native（上游收 Chat 形态，不经 Responses 转换）。
func TestChatToResponse_ChatCapableEndpointStaysNative(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-native","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"native-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	})

	resp := f.postChat(t, `{"model":"c2r-alias","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // test cleanup
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body: %s", resp.StatusCode, respBody)
	}
	if f.capture.path != "/chat/completions" {
		t.Fatalf("upstream path = %q, want /chat/completions（native）", f.capture.path)
	}
	var chatReq dto.OpenAIChatCompletionReq
	if err := sonic.Unmarshal(f.capture.lastBody(t), &chatReq); err != nil {
		t.Fatalf("upstream chat body unmarshal: %v", err)
	}
	if len(chatReq.Messages) != 1 || chatReq.Messages[0].Role != enum.RoleUser {
		t.Fatalf("upstream messages = %+v", chatReq.Messages)
	}
	if !bytes.Contains(respBody, []byte("native-ok")) {
		t.Fatalf("response = %s", respBody)
	}
	if len(f.submitter.auditTasks) != 1 || f.submitter.auditTasks[0].UpstreamProtocol != enum.ProtocolOpenAIChatCompletion {
		t.Fatalf("audit = %+v", f.submitter.auditTasks)
	}
}
