package session_share

import (
	"context"
	"reflect"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	sessionport "github.com/hcd233/aris-mass/internal/application/session/port"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/model"
	"github.com/hcd233/aris-mass/internal/dto"
	"github.com/hcd233/aris-mass/internal/handler"
	"github.com/hcd233/aris-mass/internal/infrastructure/cache"
)

type listSessionsWithShares struct {
	views []*sessionport.SessionSummaryView
}

func (m *listSessionsWithShares) Handle(_ context.Context, _ sessionport.ListSessionsByUserQuery) ([]*sessionport.SessionSummaryView, *model.PageInfo, error) {
	return m.views, &model.PageInfo{Page: 1, PageSize: 20, Total: int64(len(m.views))}, nil
}

type listShareCache struct {
	cache.ShareCache
	shareIDs map[uint]string
}

func (m *listShareCache) GetSessionShareIDs(_ context.Context, _ []uint) (map[uint]string, error) {
	return m.shareIDs, nil
}

func TestListSessionsByUser_IncludesShareID(t *testing.T) {
	t.Parallel()
	h := handler.NewSessionHandler(handler.SessionDependencies{
		ListByUser: &listSessionsWithShares{views: []*sessionport.SessionSummaryView{{ID: 1}, {ID: 2}}},
		ShareCache: &listShareCache{shareIDs: map[uint]string{1: "shared-id"}},
	})

	rsp, err := h.HandleListSessionsByUser(ctxWithUser(42), &dto.ListSessionsByUserReq{})
	if err != nil {
		t.Fatalf("HandleListSessionsByUser returned error: %v", err)
	}
	if len(rsp.Body.Sessions) != 2 {
		t.Fatalf("sessions count = %d, want 2", len(rsp.Body.Sessions))
	}
	if rsp.Body.Sessions[0].ShareID != "shared-id" {
		t.Errorf("shared session ShareID = %q, want shared-id", rsp.Body.Sessions[0].ShareID)
	}
	if rsp.Body.Sessions[1].ShareID != "" {
		t.Errorf("unshared session ShareID = %q, want empty", rsp.Body.Sessions[1].ShareID)
	}
}

func TestRedisShareCacheGetSessionShareIDs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = rdb.Close() }()
	shareCache := cache.NewShareCache(rdb)

	firstID, _, err := shareCache.CreateShare(ctx, 42, 1, constant.ShareTTLDefault)
	if err != nil {
		t.Fatalf("CreateShare session 1 failed: %v", err)
	}
	secondID, _, err := shareCache.CreateShare(ctx, 42, 2, constant.ShareTTLDefault)
	if err != nil {
		t.Fatalf("CreateShare session 2 failed: %v", err)
	}

	got, err := shareCache.GetSessionShareIDs(ctx, []uint{1, 2, 3})
	if err != nil {
		t.Fatalf("GetSessionShareIDs returned error: %v", err)
	}
	want := map[uint]string{1: firstID, 2: secondID}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetSessionShareIDs = %v, want %v", got, want)
	}

	empty, err := shareCache.GetSessionShareIDs(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("GetSessionShareIDs(nil) = %v, %v; want empty map, nil", empty, err)
	}
}
