// Package port defines application-layer ports for dataset export use cases.
package port

import (
	"bufio"
	"context"
	"time"

	"github.com/hcd233/aris-mass/internal/common/enum"
)

// ExportParams 训练数据导出参数
//
//	@author centonhuang
//	@update 2026-07-03 10:00:00
type ExportParams struct {
	Permission enum.Permission
	UserID     uint
	MinScore   int
	ModelIDs   []string
	StartTime  time.Time
	EndTime    time.Time
}

// PreviewResult 统计预览结果，语义见 session.ExportPreview
//
//	@author centonhuang
//	@update 2026-11-09 10:00:00
type PreviewResult struct {
	TotalSessions  int
	TotalMessages  int
	ToolSessions   int
	ScoreHistogram map[int]int
	ModelFacets    map[string]int
}

// PreviewDatasetHandler 统计预览处理器
//
//	@author centonhuang
//	@update 2026-07-03 10:00:00
type PreviewDatasetHandler interface {
	Handle(ctx context.Context, p ExportParams) (*PreviewResult, error)
}

// ExportStream 一次流式导出
//
//	Total 是起始查询的匹配会话数；Write 在固定 ID 上界内按页重新应用筛选，逐会话写入 ShareGPT JSONL。
//	客户端收齐 Total 条完整行且没有尾部残片时才提交下载。尺寸超限时，流末尾包含不带换行的错误说明。
//
//	@author centonhuang
//	@update 2026-11-06 10:00:00
type ExportStream struct {
	Total int
	Write func(w *bufio.Writer) error
}

// ExportDatasetHandler 流式导出处理器（NDJSON）
//
//	筛选、鉴权与"无匹配会话"等错误在返回 ExportStream 之前给出，便于以普通 JSON 错误响应。
//
//	@author centonhuang
//	@update 2026-11-06 10:00:00
type ExportDatasetHandler interface {
	Handle(ctx context.Context, p ExportParams) (*ExportStream, error)
}

// FormatPreviewResult 单条会话格式预览结果
//
//	@author centonhuang
//	@update 2026-11-09 10:00:00
type FormatPreviewResult struct {
	SessionID    uint
	CreatedAt    time.Time
	Score        *int
	ModelIDs     []string
	Offset       int
	TotalCount   int
	ShareGPTJSON string
}

// PreviewFormatDatasetHandler 单条会话格式预览处理器
//
//	@author centonhuang
//	@update 2026-07-06 10:00:00
type PreviewFormatDatasetHandler interface {
	Handle(ctx context.Context, p ExportParams, offset int) (*FormatPreviewResult, error)
}
