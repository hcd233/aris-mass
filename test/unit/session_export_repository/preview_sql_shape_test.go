// 预览统计的 SQL 形态守护（PG 方言，真实走完 PreviewExport 的三条查询）。
//
// 背景（2026-11-09 训练数据页重设计）：旧实现以 jsonb_array_elements_text(model_ids)
// 展开后逐行计数，一个会话用了 N 个模型就被计 N 次，「匹配会话数」与实际导出条数不一致；
// 多选模型用 @> 实现成「同时包含全部所选」。本测试钉住修复后的形态：
//   - 汇总查询按会话计数，不展开模型数组；
//   - 评分直方图不应用 minScore、模型分面不应用 modelIds（分面语义）；
//   - 模型筛选为「任一所选」（EXISTS … IN）。
//
// 预览 SQL 含 PG 专属语法，sqlite 无法执行；gorm DryRun 又不支持 Scan。
// 这里用一个只记录 SQL、返回空结果集的 database/sql 连接器驱动 postgres 方言。
package session_export_repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"sync"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/hcd233/aris-mass/internal/domain/session"
	"github.com/hcd233/aris-mass/internal/infrastructure/repository"
)

type capturedQuery struct {
	sql  string
	args []driver.NamedValue
}

type captureConnector struct {
	mu      sync.Mutex
	queries []capturedQuery
}

func (c *captureConnector) Connect(context.Context) (driver.Conn, error) {
	return &captureConn{c: c}, nil
}

func (c *captureConnector) Driver() driver.Driver { return captureDriver{c: c} }

type captureDriver struct{ c *captureConnector }

func (d captureDriver) Open(string) (driver.Conn, error) { return &captureConn{c: d.c}, nil }

type captureConn struct{ c *captureConnector }

func (*captureConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*captureConn) Close() error                        { return nil }
func (*captureConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

func (cc *captureConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	cc.c.mu.Lock()
	defer cc.c.mu.Unlock()
	cc.c.queries = append(cc.c.queries, capturedQuery{sql: query, args: args})
	return emptyRows{}, nil
}

type emptyRows struct{}

func (emptyRows) Columns() []string         { return []string{} }
func (emptyRows) Close() error              { return nil }
func (emptyRows) Next([]driver.Value) error { return io.EOF }

func newCaptureRepo(t *testing.T) (session.SessionReadRepository, *captureConnector) {
	t.Helper()
	connector := &captureConnector{}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sql.OpenDB(connector)}), &gorm.Config{
		Logger:               gormlogger.Default.LogMode(gormlogger.Silent),
		DisableAutomaticPing: true,
	})
	if err != nil {
		t.Fatalf("open capture pg db: %v", err)
	}
	return repository.NewSessionReadRepository(db, nil), connector
}

// findQuery 返回唯一包含 marker 的查询
func findQuery(t *testing.T, queries []capturedQuery, marker string) capturedQuery {
	t.Helper()
	var found []capturedQuery
	for _, q := range queries {
		if strings.Contains(q.sql, marker) {
			found = append(found, q)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly 1 query containing %q, got %d\nall: %+v", marker, len(found), queries)
	}
	return found[0]
}

func hasArg(q capturedQuery, want string) bool {
	for _, a := range q.args {
		if s, ok := a.Value.(string); ok && s == want {
			return true
		}
	}
	return false
}

func TestPreviewExport_SQLShape(t *testing.T) {
	t.Parallel()
	repo, capture := newCaptureRepo(t)

	preview, err := repo.PreviewExport(t.Context(), session.ExportFilter{
		MinScore: 3,
		ModelIDs: []string{"model-a", "model-b"},
		OwnerIDs: []uint{7},
	})
	if err != nil {
		t.Fatalf("PreviewExport: %v", err)
	}
	if len(capture.queries) != 3 {
		t.Fatalf("PreviewExport should issue 3 queries, got %d", len(capture.queries))
	}
	if preview.ScoreHistogram == nil || preview.ModelFacets == nil {
		t.Errorf("empty preview maps must be non-nil, got %+v", preview)
	}

	summary := findQuery(t, capture.queries, "total_sessions")
	if !strings.HasPrefix(summary.sql, "SELECT COUNT(*) AS total_sessions") || strings.Contains(summary.sql, "LATERAL") {
		t.Errorf("summary must count sessions without expanding model_ids into rows\ngot: %s", summary.sql)
	}
	for _, want := range []string{"COUNT(*) AS total_sessions", "score >=", "EXISTS", "em.model IN", "api_key_id IN", "deleted_at = 0"} {
		if !strings.Contains(summary.sql, want) {
			t.Errorf("summary missing %q\ngot: %s", want, summary.sql)
		}
	}
	if !hasArg(summary, "model-a") || !hasArg(summary, "model-b") {
		t.Errorf("model filter must bind each selected model as an IN arg (any-of), args=%v", summary.args)
	}

	histogram := findQuery(t, capture.queries, "COALESCE(score, 0) AS score")
	if strings.Contains(histogram.sql, "score >=") {
		t.Errorf("score histogram must not apply minScore\ngot: %s", histogram.sql)
	}
	for _, want := range []string{"GROUP BY COALESCE(score, 0)", "EXISTS", "api_key_id IN"} {
		if !strings.Contains(histogram.sql, want) {
			t.Errorf("histogram missing %q\ngot: %s", want, histogram.sql)
		}
	}

	facets := findQuery(t, capture.queries, "LATERAL")
	if strings.Contains(facets.sql, "EXISTS") {
		t.Errorf("model facets must not apply the model filter\ngot: %s", facets.sql)
	}
	for _, want := range []string{`GROUP BY "fm"."model"`, "score >=", "api_key_id IN"} {
		if !strings.Contains(facets.sql, want) {
			t.Errorf("facets missing %q\ngot: %s", want, facets.sql)
		}
	}
}

// TestPreviewExport_EmptyOwnerShortCircuits 名下无 Key 时不发任何查询（不得退化为全量统计）
func TestPreviewExport_EmptyOwnerShortCircuits(t *testing.T) {
	t.Parallel()
	repo, capture := newCaptureRepo(t)

	preview, err := repo.PreviewExport(t.Context(), session.ExportFilter{OwnerIDs: []uint{}})
	if err != nil {
		t.Fatalf("PreviewExport: %v", err)
	}
	if len(capture.queries) != 0 {
		t.Errorf("empty owner list must not query, got %d queries", len(capture.queries))
	}
	if preview.TotalSessions != 0 || preview.ScoreHistogram == nil || preview.ModelFacets == nil {
		t.Errorf("want empty preview with non-nil maps, got %+v", preview)
	}
}
