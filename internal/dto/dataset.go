// Package dto Dataset export DTOs
package dto

import (
	"time"
)

// DatasetPreviewReq 数据集统计预览请求（GET query 参数）
//
//	@author centonhuang
//	@update 2026-07-03 10:00:00
type DatasetPreviewReq struct {
	MinScore  int       `query:"minScore" minimum:"0" maximum:"5" doc:"最低评分阈值(0-5, 0=不筛选)"`
	ModelIDs  []string  `query:"modelIds" doc:"模型列表筛选(逗号分隔)"`
	StartTime time.Time `query:"startTime" doc:"起始时间"`
	EndTime   time.Time `query:"endTime" doc:"结束时间"`
}

// DatasetPreviewRsp 数据集统计预览响应
//
//	汇总字段应用全部筛选条件；scoreHistogram 不应用 minScore、modelFacets 不应用 modelIds（分面语义）。
//
//	@author centonhuang
//	@update 2026-11-09 10:00:00
type DatasetPreviewRsp struct {
	CommonRsp
	TotalSessions  int            `json:"totalSessions" doc:"匹配会话数（与导出条数一致）"`
	TotalMessages  int            `json:"totalMessages" doc:"匹配会话的消息总数"`
	ToolSessions   int            `json:"toolSessions" doc:"带工具定义的匹配会话数"`
	ScoreHistogram map[int]int    `json:"scoreHistogram,omitempty" doc:"评分直方图(分值→会话数，0=未评分；不应用 minScore)"`
	ModelFacets    map[string]int `json:"modelFacets,omitempty" doc:"模型分面(模型→会话数；不应用 modelIds)"`
}

// DatasetExportReq 数据集流式导出请求（GET query 参数）
//
//	@author centonhuang
//	@update 2026-07-03 10:00:00
type DatasetExportReq struct {
	MinScore  int       `query:"minScore" minimum:"0" maximum:"5" doc:"最低评分阈值(0-5, 0=不筛选)"`
	ModelIDs  []string  `query:"modelIds" doc:"模型列表筛选(逗号分隔)"`
	StartTime time.Time `query:"startTime" doc:"起始时间"`
	EndTime   time.Time `query:"endTime" doc:"结束时间"`
}

// DatasetFormatPreviewReq 单条会话格式预览请求（GET query 参数）
//
//	@author centonhuang
//	@update 2026-07-06 10:00:00
type DatasetFormatPreviewReq struct {
	MinScore  int       `query:"minScore" minimum:"0" maximum:"5" doc:"最低评分阈值(0-5, 0=不筛选)"`
	ModelIDs  []string  `query:"modelIds" doc:"模型列表筛选(逗号分隔)"`
	StartTime time.Time `query:"startTime" doc:"起始时间"`
	EndTime   time.Time `query:"endTime" doc:"结束时间"`
	Offset    int       `query:"offset" minimum:"0" doc:"会话索引偏移量"`
}

// DatasetFormatPreviewRsp 单条会话格式预览响应
//
//	@author centonhuang
//	@update 2026-11-09 10:00:00
type DatasetFormatPreviewRsp struct {
	CommonRsp
	SessionID    uint      `json:"sessionId,omitempty" doc:"会话 ID"`
	CreatedAt    time.Time `json:"createdAt" doc:"会话创建时间"`
	Score        *int      `json:"score,omitempty" doc:"会话评分(未评分时缺省)"`
	ModelIDs     []string  `json:"modelIds,omitempty" doc:"会话用到的模型"`
	Offset       int       `json:"offset" doc:"当前偏移量(越界时钳到最后一条)"`
	TotalCount   int       `json:"totalCount" doc:"匹配会话总数"`
	ShareGPTJSON string    `json:"sharegptJson,omitempty" doc:"ShareGPT 格式的一行 JSON"`
}
