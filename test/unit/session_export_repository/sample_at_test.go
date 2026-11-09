// 样本检查器逐条取样（FindSessionForExportAt）的真实执行守护（sqlite）。
//
// 旧实现每次翻页都把全部匹配会话的 id/message_ids/tool_ids 读进内存再按下标取一条；
// 改为 COUNT + OFFSET/LIMIT 1 后，钉住：按 id 升序、越界钳到末条、负数取首条、
// 归属三态语义（空列表不得退化为全量）与返回的元数据。
package session_export_repository

import (
	"testing"

	"github.com/hcd233/aris-mass/internal/domain/session"
	dbmodel "github.com/hcd233/aris-mass/internal/infrastructure/database/model"
	"github.com/hcd233/aris-mass/internal/infrastructure/repository"
)

func TestFindSessionForExportAt_OffsetAndScope(t *testing.T) {
	t.Parallel()

	four := 4
	db := newExportTestDB(t, []*dbmodel.Session{
		{APIKeyID: 1, ModelIDs: []string{"a-first"}, MessageIDs: []uint{1}, Score: &four},
		{APIKeyID: 2, ModelIDs: []string{"b-only"}, MessageIDs: []uint{2}},
		{APIKeyID: 1, ModelIDs: []string{"a-second"}, MessageIDs: []uint{3, 4}},
	})
	repo := repository.NewSessionReadRepository(db, nil)
	ctx := t.Context()
	tenantA := session.ExportFilter{OwnerIDs: []uint{1}}

	cases := []struct {
		name      string
		offset    int
		wantModel string
	}{
		{"first", 0, "a-first"},
		{"second", 1, "a-second"},
		{"beyond end clamps to last", 99, "a-second"},
		{"negative clamps to first", -5, "a-first"},
	}
	for _, tc := range cases {
		row, total, err := repo.FindSessionForExportAt(ctx, tenantA, tc.offset)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if total != 2 {
			t.Errorf("%s: total = %d; want 2 (tenant A only)", tc.name, total)
		}
		if row == nil || len(row.ModelIDs) != 1 || row.ModelIDs[0] != tc.wantModel {
			t.Fatalf("%s: row = %+v; want model %q", tc.name, row, tc.wantModel)
		}
	}

	first, _, err := repo.FindSessionForExportAt(ctx, tenantA, 0)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.Score == nil || *first.Score != 4 || first.CreatedAt.IsZero() || len(first.MessageIDs) != 1 {
		t.Errorf("first row metadata not populated: %+v", first)
	}

	row, total, err := repo.FindSessionForExportAt(ctx, session.ExportFilter{OwnerIDs: []uint{}}, 0)
	if err != nil {
		t.Fatalf("empty owner: %v", err)
	}
	if row != nil || total != 0 {
		t.Errorf("empty owner list must match nothing, got row=%+v total=%d", row, total)
	}

	_, total, err = repo.FindSessionForExportAt(ctx, session.ExportFilter{}, 0)
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	if total != 3 {
		t.Errorf("nil owner list (admin) total = %d; want 3", total)
	}
}
