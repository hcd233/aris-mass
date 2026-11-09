package repository

import (
	"context"
	"time"

	"github.com/bytedance/sonic"
	"github.com/samber/lo"
	"gorm.io/gorm"

	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/ierr"
	"github.com/hcd233/aris-mass/internal/common/vo"
	"github.com/hcd233/aris-mass/internal/domain/conversation"
	"github.com/hcd233/aris-mass/internal/infrastructure/database/dao"
	dbmodel "github.com/hcd233/aris-mass/internal/infrastructure/database/model"
)

// messageRepository ThinkExtractRepository 的 GORM 实现
type messageRepository struct {
	dao *dao.MessageDAO
	db  *gorm.DB
}

// NewThinkExtractRepository 构造推理内容提取仓储
//
//	@param db *gorm.DB
//	@return conversation.ThinkExtractRepository
func NewThinkExtractRepository(db *gorm.DB) conversation.ThinkExtractRepository {
	return &messageRepository{dao: dao.GetMessageDAO(), db: db}
}

func (r *messageRepository) FindThinkExtractCandidates(ctx context.Context, afterID uint, startTime, endTime time.Time, limit int) ([]*conversation.ThinkExtractMessage, error) {
	if limit < 1 {
		limit = 1
	}
	db := r.db.WithContext(ctx)
	var records []*dbmodel.Message
	query := db.Model(&dbmodel.Message{}).
		Select([]string{constant.FieldID, constant.FieldMessage}).
		Where(constant.DBConditionIDGreaterThan, afterID).
		Where(constant.DBConditionDeletedAtZero).
		Where(constant.DBJSONConditionAssistantRole).
		Where(constant.DBJSONConditionHasThinkTag).
		Where(constant.DBJSONConditionReasoningEmpty)
	if !startTime.IsZero() {
		query = query.Where(constant.FieldCreatedAt+" >= ?", startTime)
	}
	if !endTime.IsZero() {
		query = query.Where(constant.FieldCreatedAt+" < ?", endTime)
	}
	if err := query.Order(constant.DBOrderByID).Limit(limit).Find(&records).Error; err != nil {
		return nil, ierr.Wrap(ierr.ErrDBQuery, err, "query think extract messages")
	}
	out := lo.Map(records, func(record *dbmodel.Message, _ int) *conversation.ThinkExtractMessage {
		return &conversation.ThinkExtractMessage{ID: record.ID, Message: record.Message}
	})
	return out, nil
}

func (r *messageRepository) UpdateMessageContent(ctx context.Context, id uint, message *vo.UnifiedMessage) error {
	db := r.db.WithContext(ctx)
	messageJSON, err := sonic.Marshal(message)
	if err != nil {
		return ierr.Wrap(ierr.ErrDBUpdate, err, "marshal message content")
	}
	updates := map[string]any{
		constant.FieldMessage:   string(messageJSON),
		constant.FieldUpdatedAt: time.Now().UTC(),
	}
	if err := db.Model(&dbmodel.Message{ID: id}).Select([]string{constant.FieldMessage, constant.FieldUpdatedAt}).Updates(updates).Error; err != nil {
		return ierr.Wrap(ierr.ErrDBUpdate, err, "update message content")
	}
	return nil
}
