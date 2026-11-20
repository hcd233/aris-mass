// Package session_export_repository 验证导出链路（ListSessionsForExport / PreviewExport）
// 的归属范围语义：非 nil 空 OwnerIDs 必须返回空结果，不得退化为无过滤全量查询。
//
// 背景一（2026-08-25 越权修复，最高危）：旧实现 applyExportFilter 用
// `if len(f.OwnerNames) > 0` 决定是否加归属过滤，用户名下无 API Key 时
// 过滤被整体跳过——普通用户可经数据集导出/统计预览/格式预览三个接口
// 越权导出全平台所有用户的完整会话内容。
//
// 背景二（2026-09-27）：归属原按 api_key_name 过滤，而该名称可跨用户重复，
// 普通用户建同名 Key 即可导出他人会话。改为按 api_key_id 过滤。
// 本包是导出路径跨租户同名场景的唯一真实执行守护：E2E 的导出预览 SQL 含
// PG 专属语法无法在 sqlite 运行（见 test/e2e/cross_tenant_session 注释）。
//
// PreviewExport 与 ListSessionsForExport 共用 applyExportFilter，本测试仅真实执行
// 后者（前者的 SELECT 含 jsonb_array_elements_text 等 PG 专属语法，sqlite 无法解析）。
package session_export_repository

import (
	"context"
	"slices"
	"testing"

	"github.com/hcd233/aris-mass/internal/domain/session"
	dbmodel "github.com/hcd233/aris-mass/internal/infrastructure/database/model"
	"github.com/hcd233/aris-mass/internal/infrastructure/repository"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newExportTestDB(t *testing.T, seed []*dbmodel.Session) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	if err := db.AutoMigrate(&dbmodel.Session{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	if err := db.Create(seed).Error; err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	return db
}

// TestListSessionsForExport_OwnerScopeSemantics 三态语义：
// nil（admin 全量）/ 空 slice（名下无 Key，必须空）/ 具体值（按归属 ID 过滤）。
func TestListSessionsForExport_OwnerScopeSemantics(t *testing.T) {
	t.Parallel()

	db := newExportTestDB(t, []*dbmodel.Session{
		{APIKeyName: "owner-a", APIKeyID: 1, ModelIDs: []string{"gpt-4"}},
		{APIKeyName: "owner-b", APIKeyID: 2, ModelIDs: []string{"gpt-4"}},
		{APIKeyName: "owner-b", APIKeyID: 2, ModelIDs: []string{"claude"}},
	})
	repo := repository.NewSessionReadRepository(db, nil)
	ctx := context.Background()

	// 空（非 nil）OwnerIDs：用户名下无 Key，必须返回空（不得导出全平台会话）
	empty := session.ExportFilter{OwnerIDs: []uint{}}
	bounds, err := repo.GetSessionExportBounds(ctx, empty)
	if err != nil || bounds.Total != 0 || bounds.ThroughID != 0 {
		t.Fatalf("GetSessionExportBounds(empty) = %+v, err=%v", bounds, err)
	}
	rows, err := repo.ListSessionsForExport(ctx, empty, 0, 3)
	if err != nil {
		t.Fatalf("ListSessionsForExport(empty) err: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("ListSessionsForExport(empty) = %d rows; want 0", len(rows))
	}

	// 具体归属：只返回该 ID 名下的
	rows, err = repo.ListSessionsForExport(ctx, session.ExportFilter{OwnerIDs: []uint{2}}, 0, 3)
	if err != nil {
		t.Fatalf("ListSessionsForExport(id=2) err: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("ListSessionsForExport(id=2) = %d rows; want 2", len(rows))
	}

	// nil：admin 全量
	rows, err = repo.ListSessionsForExport(ctx, session.ExportFilter{}, 0, 3)
	if err != nil {
		t.Fatalf("ListSessionsForExport(nil) err: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("ListSessionsForExport(nil) = %d rows; want 3", len(rows))
	}
}

// TestListSessionsForExport_SameKeyNameAcrossTenants 跨用户同名 Key 不得互相导出。
//
// 两个用户各持一个名为 "shared-name" 的 Key（唯一索引是 (user_id, name)，合法），
// 按名称过滤会让任一方导出双方全部会话。按 ID 过滤必须严格隔离；
// 空归属（api_key_id=0）会话对任何非 nil 归属列表均不可见。
func TestListSessionsForExport_SameKeyNameAcrossTenants(t *testing.T) {
	t.Parallel()

	seed := []*dbmodel.Session{
		{APIKeyName: "shared-name", APIKeyID: 1, ModelIDs: []string{"tenant-a-model"}},
		{APIKeyName: "shared-name", APIKeyID: 2, ModelIDs: []string{"tenant-b-model"}},
		{APIKeyName: "", APIKeyID: 0, ModelIDs: []string{"orphan-model"}},
	}
	db := newExportTestDB(t, seed)
	repo := repository.NewSessionReadRepository(db, nil)
	ctx := context.Background()

	rows, err := repo.ListSessionsForExport(ctx, session.ExportFilter{OwnerIDs: []uint{2}}, 0, seed[2].ID)
	if err != nil {
		t.Fatalf("ListSessionsForExport(tenant-b) err: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("tenant B must export exactly its own 1 session, got %d rows", len(rows))
	}
	if rows[0].ID != seed[1].ID {
		t.Errorf("tenant B exported foreign session, id=%d", rows[0].ID)
	}
}

func TestSessionExportPagesExcludeSessionsInsertedAfterBounds(t *testing.T) {
	t.Parallel()

	const matching = 53
	seed := []*dbmodel.Session{{APIKeyID: 8}}
	var expected []uint
	for range matching {
		seed = append(seed, &dbmodel.Session{APIKeyID: 7})
	}
	db := newExportTestDB(t, seed)
	for _, row := range seed[1:] {
		expected = append(expected, row.ID)
	}
	repo := repository.NewSessionReadRepository(db, nil)
	ctx := context.Background()
	filter := session.ExportFilter{OwnerIDs: []uint{7}}
	bounds, err := repo.GetSessionExportBounds(ctx, filter)
	if err != nil || bounds.Total != matching || bounds.ThroughID != expected[len(expected)-1] {
		t.Fatalf("bounds=%+v err=%v", bounds, err)
	}
	if err := db.Create(&dbmodel.Session{APIKeyID: 7}).Error; err != nil {
		t.Fatalf("insert after bounds: %v", err)
	}

	first, err := repo.ListSessionsForExport(ctx, filter, 0, bounds.ThroughID)
	if err != nil || len(first) == 0 || len(first) >= matching {
		t.Fatalf("expected a bounded first page, rows=%d err=%v", len(first), err)
	}
	second, err := repo.ListSessionsForExport(ctx, filter, first[len(first)-1].ID, bounds.ThroughID)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	var actual []uint
	for _, row := range append(first, second...) {
		actual = append(actual, row.ID)
	}
	if !slices.Equal(actual, expected) {
		t.Fatalf("ids=%v want=%v", actual, expected)
	}
}

func TestSessionExportPageOmitsOversizedReferenceArrays(t *testing.T) {
	t.Parallel()

	seed := []*dbmodel.Session{{APIKeyID: 1, MessageIDs: make([]uint, 132_000), ToolIDs: []uint{7}}}
	db := newExportTestDB(t, seed)
	repo := repository.NewSessionReadRepository(db, nil)
	page, err := repo.ListSessionsForExport(context.Background(), session.ExportFilter{}, 0, seed[0].ID)
	if err != nil || len(page) != 1 {
		t.Fatalf("page rows=%d err=%v", len(page), err)
	}
	got := page[0]
	if !got.OverBudget || got.ID != seed[0].ID || len(got.MessageIDs)+len(got.ToolIDs) != 0 {
		t.Fatalf("id=%d overBudget=%t messageIDs=%d toolIDs=%d", got.ID, got.OverBudget, len(got.MessageIDs), len(got.ToolIDs))
	}
}
