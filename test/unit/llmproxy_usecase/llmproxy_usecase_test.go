package llmproxy_usecase

import (
	"context"
	"io"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/hcd233/aris-mass/internal/application/llmproxy/usecase"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy/aggregate"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy/service"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy/vo"
	"github.com/hcd233/aris-mass/internal/dto"
	dbmodel "github.com/hcd233/aris-mass/internal/infrastructure/database/model"
	"github.com/hcd233/aris-mass/internal/infrastructure/repository"
)

type mockReadRepo struct {
	listAliasesResult []*llmproxy.ModelAliasProjection
	listAliasesErr    error
}

func newMockReadRepo(aliases []string) *mockReadRepo {
	projections := make([]*llmproxy.ModelAliasProjection, len(aliases))
	for i, alias := range aliases {
		projections[i] = &llmproxy.ModelAliasProjection{Alias: alias}
	}
	return &mockReadRepo{
		listAliasesResult: projections,
	}
}

func (r *mockReadRepo) ListAliases(_ context.Context, _ uint) ([]*llmproxy.ModelAliasProjection, error) {
	return r.listAliasesResult, r.listAliasesErr
}

func (r *mockReadRepo) ListEnabledModelDetails(_ context.Context, _ uint) ([]*llmproxy.ModelDetailProjection, error) {
	return nil, nil
}

type mockAnthropicProxy struct {
	forwardCountTokensCalled bool
	forwardCountTokensResult *dto.AnthropicTokensCount
	forwardCountTokensErr    error
	lastEndpoint             vo.UpstreamEndpoint
}

func (p *mockAnthropicProxy) ForwardCountTokens(_ context.Context, ep vo.UpstreamEndpoint, _ []byte) (*dto.AnthropicTokensCount, error) {
	p.forwardCountTokensCalled = true
	p.lastEndpoint = ep
	return p.forwardCountTokensResult, p.forwardCountTokensErr
}

func (p *mockAnthropicProxy) OpenCreateMessageStream(_ context.Context, _ vo.UpstreamEndpoint, _ []byte) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (p *mockAnthropicProxy) ReadCreateMessageStream(_ context.Context, _ io.ReadCloser, _ func(dto.AnthropicSSEEvent) error) (*dto.AnthropicMessage, error) {
	return nil, nil
}

func (p *mockAnthropicProxy) ForwardCreateMessage(_ context.Context, _ vo.UpstreamEndpoint, _ []byte) (*dto.AnthropicMessage, error) {
	return nil, nil
}

var _ usecase.AnthropicProxyPort = (*mockAnthropicProxy)(nil)

func TestListOpenAIModels_Success(t *testing.T) {
	t.Parallel()
	repo := newMockReadRepo([]string{"gpt-4o", "gpt-4o-mini", "gpt-4-turbo"})
	query := usecase.NewListOpenAIModels(repo)

	rsp, err := query.Handle(context.Background())
	if err != nil {
		t.Fatalf("Handle() error: %v", err)
	}
	if rsp == nil {
		t.Fatal("Handle() returned nil response")
		return
	}
	if len(rsp.Data) != 3 {
		t.Errorf("expected 3 models, got %d", len(rsp.Data))
	}
	for i, model := range rsp.Data {
		if model.Object != "model" {
			t.Errorf("Data[%d].Object = %q, want %q", i, model.Object, "model")
		}
	}
}

func TestListOpenAIModels_Empty(t *testing.T) {
	t.Parallel()
	repo := newMockReadRepo([]string{})
	query := usecase.NewListOpenAIModels(repo)

	rsp, err := query.Handle(context.Background())
	if err != nil {
		t.Fatalf("Handle() error: %v", err)
	}
	if len(rsp.Data) != 0 {
		t.Errorf("expected 0 models, got %d", len(rsp.Data))
	}
}

func TestListAnthropicModels_Success(t *testing.T) {
	t.Parallel()
	repo := newMockReadRepo([]string{"claude-sonnet-4-20250514", "claude-3-5-sonnet-20241022"})
	query := usecase.NewListAnthropicModels(repo)

	rsp, err := query.Handle(context.Background())
	if err != nil {
		t.Fatalf("Handle() error: %v", err)
	}
	if rsp == nil {
		t.Fatal("Handle() returned nil response")
		return
	}
	if len(rsp.Data) != 2 {
		t.Errorf("expected 2 models, got %d", len(rsp.Data))
	}
	if rsp.HasMore != false {
		t.Errorf("HasMore = %v, want false", rsp.HasMore)
	}
}

func TestListAnthropicModels_Empty(t *testing.T) {
	t.Parallel()
	repo := newMockReadRepo([]string{})
	query := usecase.NewListAnthropicModels(repo)

	rsp, err := query.Handle(context.Background())
	if err != nil {
		t.Fatalf("Handle() error: %v", err)
	}
	if len(rsp.Data) != 0 {
		t.Errorf("expected 0 models, got %d", len(rsp.Data))
	}
	if rsp.FirstID != "" {
		t.Errorf("FirstID = %q, want empty", rsp.FirstID)
	}
	if rsp.LastID != "" {
		t.Errorf("LastID = %q, want empty", rsp.LastID)
	}
}

func TestListModels_Pagination(t *testing.T) {
	t.Parallel()
	aliases := []string{"model-a", "model-b", "model-c"}
	repo := newMockReadRepo(aliases)
	query := usecase.NewListAnthropicModels(repo)

	rsp, err := query.Handle(context.Background())
	if err != nil {
		t.Fatalf("Handle() error: %v", err)
	}
	if rsp.FirstID != "model-a" {
		t.Errorf("FirstID = %q, want %q", rsp.FirstID, "model-a")
	}
	if rsp.LastID != "model-c" {
		t.Errorf("LastID = %q, want %q", rsp.LastID, "model-c")
	}
}

func TestCountTokens_ModelNotFound(t *testing.T) {
	t.Parallel()
	proxy := &mockAnthropicProxy{}
	query := usecase.NewCountTokens(&multiCandidateResolver{}, proxy, nil)

	req := &dto.AnthropicCountTokensRequest{
		Body: &dto.AnthropicCountTokensReq{
			Model: "nonexistent-model",
		},
	}

	ctx := context.WithValue(t.Context(), constant.CtxKeyUserID, uint(1))
	rsp, err := query.Handle(ctx, req)
	if err != nil {
		t.Fatalf("Handle() error: %v", err)
	}
	if rsp.InputTokens != 0 {
		t.Errorf("expected 0 input tokens, got %d", rsp.InputTokens)
	}
	if proxy.forwardCountTokensCalled {
		t.Error("ForwardCountTokens should not be called when model not found")
	}
}

func TestCountTokensFollowsOwnEndpointPriority(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite connection: %v", err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.AutoMigrate(&dbmodel.Endpoint{}, &dbmodel.Model{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const userID uint = 42
	const alias = "claude"
	primary := dbmodel.Endpoint{
		UserID: userID, Name: "primary", AnthropicBaseURL: "https://primary.example.com",
		APIKey: "test-primary", SupportAnthropicMessage: true,
	}
	secondary := dbmodel.Endpoint{
		UserID: userID, Name: "secondary", AnthropicBaseURL: "https://secondary.example.com",
		APIKey: "test-secondary", SupportAnthropicMessage: true,
	}
	for _, endpoint := range []*dbmodel.Endpoint{&primary, &secondary} {
		if err := db.Create(endpoint).Error; err != nil {
			t.Fatalf("create endpoint: %v", err)
		}
	}
	for _, model := range []dbmodel.Model{
		{UserID: userID, Alias: alias, EndpointID: primary.ID, UpstreamModel: "primary-model", Enabled: true, Priority: 1},
		{UserID: userID, Alias: alias, EndpointID: secondary.ID, UpstreamModel: "secondary-model", Enabled: true, Priority: 2},
	} {
		if err := db.Create(&model).Error; err != nil {
			t.Fatalf("create model: %v", err)
		}
	}
	proxy := &mockAnthropicProxy{forwardCountTokensResult: &dto.AnthropicTokensCount{InputTokens: 10}}
	resolver := service.NewEndpointResolver(repository.NewEndpointRepository(db), repository.NewModelRepository(db), false, nil)
	query := usecase.NewCountTokens(resolver, proxy, nil)
	ctx := context.WithValue(t.Context(), constant.CtxKeyUserID, userID)
	req := &dto.AnthropicCountTokensRequest{Body: &dto.AnthropicCountTokensReq{Model: alias}}

	for range 24 {
		rsp, err := query.Handle(ctx, req)
		if err != nil || rsp == nil || rsp.InputTokens != 10 {
			t.Fatalf("Handle() = (%+v, %v), want 10 tokens", rsp, err)
		}
		if got := proxy.lastEndpoint; got.Model != "primary-model" || got.APIKey != primary.APIKey {
			t.Fatalf("resolved model = %q, want primary model and its credential", got.Model)
		}
	}
}

type stubTokenProvider struct {
	access service.OAuthAccess
	err    error
	gotID  uint
}

func (p *stubTokenProvider) GetAccessToken(_ context.Context, endpointID uint) (service.OAuthAccess, error) {
	p.gotID = endpointID
	return p.access, p.err
}

func (p *stubTokenProvider) RefreshIfNeeded(_ context.Context, _ uint) error { return nil }

// TestCountTokens_OAuthEndpointUsesAccessToken 守护 OAuth 端点计数使用 access token。
func TestCountTokens_OAuthEndpointUsesAccessToken(t *testing.T) {
	t.Parallel()
	tokens := &stubTokenProvider{access: service.OAuthAccess{Token: "oauth-access-token", AccountRef: "account-1"}}
	ep, err := aggregate.CreateEndpoint(7, "oauth", "", "https://api.anthropic.com", "", false, false, true, false, enum.EndpointAuthOAuthAnthropic)
	if err != nil {
		t.Fatal(err)
	}
	m, err := aggregate.CreateModel(1, "claude", "claude-upstream", 7, true, 0, 0, []enum.InputModality{enum.InputModalityText})
	if err != nil {
		t.Fatal(err)
	}
	resolver := &multiCandidateResolver{cands: []service.Candidate{{Endpoint: ep, Model: m}}}
	proxy := &mockAnthropicProxy{forwardCountTokensResult: &dto.AnthropicTokensCount{InputTokens: 42}}
	query := usecase.NewCountTokens(resolver, proxy, tokens)

	ctx := context.WithValue(t.Context(), constant.CtxKeyUserID, uint(1))
	rsp, err := query.Handle(ctx, &dto.AnthropicCountTokensRequest{
		Body: &dto.AnthropicCountTokensReq{Model: "claude"},
	})
	if err != nil {
		t.Fatalf("Handle() error: %v", err)
	}
	if rsp.InputTokens != 42 {
		t.Errorf("InputTokens = %d, want 42", rsp.InputTokens)
	}
	if tokens.gotID != 7 {
		t.Errorf("GetAccessToken endpoint id = %d, want 7", tokens.gotID)
	}
	if proxy.lastEndpoint.APIKey != "oauth-access-token" {
		t.Errorf("upstream api key = %q, want oauth access token", proxy.lastEndpoint.APIKey)
	}
	if proxy.lastEndpoint.AuthType != enum.EndpointAuthOAuthAnthropic {
		t.Errorf("upstream auth type = %q, want %q", proxy.lastEndpoint.AuthType, enum.EndpointAuthOAuthAnthropic)
	}
	if proxy.lastEndpoint.OAuthAccountID != "account-1" {
		t.Errorf("upstream account id = %q, want account-1", proxy.lastEndpoint.OAuthAccountID)
	}
}
