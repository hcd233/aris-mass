package llmproxy_usecase

import (
	"testing"

	"github.com/hcd233/aris-mass/internal/application/llmproxy/usecase"
	"github.com/hcd233/aris-mass/internal/common/enum"
)

// Chat 入口命中 Responses-only 端点（如 Codex 订阅）时应走 Chat→Response 转换，
// 而不是被候选过滤掉（此前返回 model not found）。
func TestSelectCompatRoute_ChatFallsBackToResponse(t *testing.T) {
	t.Parallel()
	ep := buildCompatEndpoint("response-only", false, true, false)
	if route := usecase.SelectCompatRoute(enum.ProxyAPIOpenAIChat, ep); route != enum.CompatRouteViaOpenAIResponse {
		t.Fatalf("route = %v, want via response", route)
	}
}

// 存量双能力端点的选路优先级不得被新分支改变。
func TestSelectCompatRoute_ChatResponseFallbackDoesNotChangeExistingPriority(t *testing.T) {
	t.Parallel()

	chatAndResponse := buildCompatEndpoint("chat-and-response", true, true, false)
	if route := usecase.SelectCompatRoute(enum.ProxyAPIOpenAIChat, chatAndResponse); route != enum.CompatRouteNative {
		t.Fatalf("chat+response route = %v, want native", route)
	}

	anthropicAndResponse := buildCompatEndpoint("anthropic-and-response", false, true, true)
	if route := usecase.SelectCompatRoute(enum.ProxyAPIOpenAIChat, anthropicAndResponse); route != enum.CompatRouteViaAnthropicMessage {
		t.Fatalf("anthropic+response route = %v, want via anthropic", route)
	}
}

// 其余入口对 Responses-only 端点的选路保持不变。
func TestSelectCompatRoute_ResponseOnlyEndpointOtherAPIs(t *testing.T) {
	t.Parallel()
	ep := buildCompatEndpoint("response-only", false, true, false)

	if route := usecase.SelectCompatRoute(enum.ProxyAPIOpenAIResponse, ep); route != enum.CompatRouteNative {
		t.Fatalf("response route = %v, want native", route)
	}
	if route := usecase.SelectCompatRoute(enum.ProxyAPIAnthropicMessage, ep); route != enum.CompatRouteUnsupported {
		t.Fatalf("anthropic route = %v, want unsupported", route)
	}
	if route := usecase.SelectCompatRoute(enum.ProxyAPIOpenAIDecision, ep); route != enum.CompatRouteUnsupported {
		t.Fatalf("decision route = %v, want unsupported", route)
	}
}
