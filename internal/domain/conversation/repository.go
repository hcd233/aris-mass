// Package conversation Conversation 域根（仓储接口）
package conversation

import (
	"context"
	"time"

	"github.com/hcd233/aris-mass/internal/common/vo"
)

// ThinkExtractMessage 推理内容提取任务的消息候选。
type ThinkExtractMessage struct {
	ID      uint
	Message *vo.UnifiedMessage
}

// ThinkExtractRepository 为定时任务暴露消息提取所需的最小仓储端口。
type ThinkExtractRepository interface {
	FindThinkExtractCandidates(ctx context.Context, afterID uint, startTime, endTime time.Time, limit int) ([]*ThinkExtractMessage, error)
	UpdateMessageContent(ctx context.Context, id uint, message *vo.UnifiedMessage) error
}
