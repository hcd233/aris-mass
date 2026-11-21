package usecase

import (
	"context"
	"time"

	"github.com/samber/lo"
	"go.uber.org/zap"

	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy/aggregate"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy/service"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy/vo"
	"github.com/hcd233/aris-mass/internal/dto"

	proxyutil "github.com/hcd233/aris-mass/internal/application/llmproxy/util"
	"github.com/hcd233/aris-mass/internal/logger"
	"github.com/hcd233/aris-mass/internal/util"
)

// ListOpenAIModels 列出 OpenAI 兼容模型
type ListOpenAIModels interface {
	Handle(ctx context.Context) (*dto.OpenAIListModelsRsp, error)
}

type listOpenAIModels struct {
	readRepo llmproxy.EndpointReadRepository
}

func NewListOpenAIModels(readRepo llmproxy.EndpointReadRepository) ListOpenAIModels {
	return &listOpenAIModels{readRepo: readRepo}
}

func (q *listOpenAIModels) Handle(ctx context.Context) (*dto.OpenAIListModelsRsp, error) {
	projections, err := q.readRepo.ListAliases(ctx, util.CtxValueUint(ctx, constant.CtxKeyUserID))
	if err != nil {
		logger.WithCtx(ctx).Error("[OpenAIQuery] Failed to query models", logger.ErrorFields(err)...)
		return &dto.OpenAIListModelsRsp{Object: constant.OpenAIListObject, Data: []*dto.OpenAIModel{}}, nil
	}
	return &dto.OpenAIListModelsRsp{
		Object: constant.OpenAIListObject,
		Data: lo.Map(projections, func(p *llmproxy.ModelAliasProjection, _ int) *dto.OpenAIModel {
			return &dto.OpenAIModel{
				ID:      p.Alias,
				Created: time.Now().Unix(),
				Object:  constant.OpenAIModelObject,
				OwnedBy: constant.OpenAIModelOwnedBy,
			}
		}),
	}, nil
}

// ListAnthropicModels 列出 Anthropic 兼容模型
type ListAnthropicModels interface {
	Handle(ctx context.Context) (*dto.AnthropicListModelsRsp, error)
}

type listAnthropicModels struct {
	readRepo llmproxy.EndpointReadRepository
}

func NewListAnthropicModels(readRepo llmproxy.EndpointReadRepository) ListAnthropicModels {
	return &listAnthropicModels{readRepo: readRepo}
}

func (q *listAnthropicModels) Handle(ctx context.Context) (*dto.AnthropicListModelsRsp, error) {
	projections, err := q.readRepo.ListAliases(ctx, util.CtxValueUint(ctx, constant.CtxKeyUserID))
	if err != nil {
		logger.WithCtx(ctx).Error("[AnthropicQuery] Failed to query models", logger.ErrorFields(err)...)
		return &dto.AnthropicListModelsRsp{Data: []*dto.AnthropicModelInfo{}}, nil
	}
	models := lo.Map(projections, func(p *llmproxy.ModelAliasProjection, _ int) *dto.AnthropicModelInfo {
		return &dto.AnthropicModelInfo{
			ID:          p.Alias,
			CreatedAt:   time.Now().UTC().Format(time.RFC3339),
			DisplayName: p.Alias,
			Type:        constant.AnthropicModelType,
		}
	})
	rsp := &dto.AnthropicListModelsRsp{Data: models, HasMore: false}
	if len(models) > 0 {
		rsp.FirstID = models[0].ID
		rsp.LastID = models[len(models)-1].ID
	}
	return rsp, nil
}

// CountTokens Anthropic /v1/messages/count_tokens
type CountTokens interface {
	Handle(ctx context.Context, req *dto.AnthropicCountTokensRequest) (*dto.AnthropicTokensCount, error)
}

type countTokens struct {
	resolver      service.EndpointResolver
	proxy         AnthropicProxyPort
	tokenProvider service.OAuthTokenProvider
}

func NewCountTokens(resolver service.EndpointResolver, proxy AnthropicProxyPort, tokenProvider service.OAuthTokenProvider) CountTokens {
	return &countTokens{resolver: resolver, proxy: proxy, tokenProvider: tokenProvider}
}

func (q *countTokens) Handle(ctx context.Context, req *dto.AnthropicCountTokensRequest) (*dto.AnthropicTokensCount, error) {
	log := logger.WithCtx(ctx)

	userID := util.CtxValueUint(ctx, constant.CtxKeyUserID)
	if userID == 0 {
		return &dto.AnthropicTokensCount{}, nil
	}
	candidates, err := q.resolver.ResolveCandidates(ctx, userID, vo.EndpointAlias(req.Body.Model), (*aggregate.Endpoint).SupportAnthropicMessage)
	if err != nil {
		log.Warn("[AnthropicQuery] Model lookup error, returning 0", append(logger.ErrorFields(err), zap.String("model", req.Body.Model))...)
		return &dto.AnthropicTokensCount{}, nil
	}
	if len(candidates) == 0 {
		log.Warn("[AnthropicQuery] Model not found, returning 0", zap.String("model", req.Body.Model))
		return &dto.AnthropicTokensCount{}, nil
	}
	candidate := candidates[0]
	ctx = context.WithValue(ctx, constant.CtxKeyEndpointAttempt, uint(1))
	log.Info("[AnthropicQuery] Trying endpoint",
		zap.String("event", "llm.route.attempt"),
		zap.String("model", logger.Identifier(req.Body.Model)),
		zap.Uint("endpointID", candidate.Endpoint.AggregateID()),
		zap.String("endpoint", candidate.Endpoint.Name()),
		zap.String("upstreamModel", logger.Identifier(candidate.Model.UpstreamModel())),
		zap.Int("endpointAttempt", 1),
		zap.Int("candidateCount", len(candidates)),
	)
	upstream, err := buildTransportEndpointWithToken(ctx, candidate.Model, candidate.Endpoint, true, q.tokenProvider)
	if err != nil {
		log.Warn("[AnthropicQuery] Build upstream endpoint failed", append(logger.ErrorFields(err), zap.String("model", req.Body.Model))...)
		return nil, err
	}
	body := proxyutil.MarshalAnthropicCountTokensBodyForModel(req.Body, upstream.Model)

	rsp, err := q.proxy.ForwardCountTokens(ctx, upstream, body)
	if err != nil {
		log.Warn("[AnthropicQuery] Count tokens error, returning 0", logger.ErrorFields(err)...)
		return &dto.AnthropicTokensCount{}, nil
	}
	return rsp, nil
}
