package command

import (
	"context"

	"go.uber.org/zap"

	"github.com/hcd233/aris-mass/internal/application/endpoint/port"
	"github.com/hcd233/aris-mass/internal/common/ierr"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy/aggregate"
	"github.com/hcd233/aris-mass/internal/logger"
	"github.com/hcd233/aris-mass/internal/util"
)

type updateEndpointHandler struct {
	repo llmproxy.EndpointRepository
}

// NewUpdateEndpointHandler 构造更新命令处理器
func NewUpdateEndpointHandler(repo llmproxy.EndpointRepository) port.UpdateEndpointHandler {
	return &updateEndpointHandler{repo: repo}
}

// Handle 执行更新命令
func (h *updateEndpointHandler) Handle(ctx context.Context, cmd port.UpdateEndpointCommand) error {
	log := logger.WithCtx(ctx)

	if cmd.Visibility != nil && !cmd.IsAdmin {
		return ierr.New(ierr.ErrUnauthorized, "only admin can set endpoint visibility")
	}

	// baseURL SSRF 校验放应用层且先于仓储查询：既避免非法 URL 触达 repo，
	// 也避免"URL 非法"与"端点不存在"的报错差异被用来探测端点 ID。
	// nil/空串表示不改或清空，不产生服务端请求目标，交由聚合根既有规则。
	for _, raw := range []*string{cmd.OpenaiBaseURL, cmd.AnthropicBaseURL} {
		if raw == nil || *raw == "" {
			continue
		}
		if err := util.ValidateEndpointBaseURL(*raw); err != nil {
			return err
		}
	}

	err := h.repo.Patch(ctx, cmd.EndpointID, cmd.ScopeUserID, cmd.Visibility != nil, func(ep *aggregate.Endpoint) error {
		// 降级后的普通用户仍能命中自己名下的公开端点。
		if ep.Visibility().IsPublic() && !cmd.IsAdmin {
			return ierr.New(ierr.ErrUnauthorized, "only admin can modify a public endpoint")
		}
		if cmd.Visibility != nil && ep.UserID() != cmd.OperatorUserID {
			return ierr.New(ierr.ErrUnauthorized, "only the owner can change endpoint visibility")
		}
		if err := ep.Update(cmd.Name, cmd.OpenaiBaseURL, cmd.AnthropicBaseURL, cmd.APIKey, cmd.SupportOpenAIChatCompletion, cmd.SupportOpenAIResponse, cmd.SupportAnthropicMessage, cmd.SupportOpenAIDecision); err != nil {
			return err
		}
		if cmd.Visibility != nil {
			return ep.SetVisibility(*cmd.Visibility)
		}
		return nil
	})
	if err != nil {
		log.Error("[EndpointCommand] Update endpoint failed", zap.Error(err))
		return err
	}

	log.Info("[EndpointCommand] Update endpoint success", zap.Uint("id", cmd.EndpointID))
	return nil
}
