package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/ierr"
	"github.com/hcd233/aris-mass/internal/domain/session"
	dbmodel "github.com/hcd233/aris-mass/internal/infrastructure/database/model"
)

// GetSessionExportBounds 从同一数据库快照取得总数及 ID 上界，不读取会话内容。
func (r *sessionReadRepository) GetSessionExportBounds(ctx context.Context, f session.ExportFilter) (session.ExportBounds, error) {
	var bounds session.ExportBounds
	if f.OwnerIDs != nil && len(f.OwnerIDs) == 0 {
		return bounds, nil
	}
	query := r.db.WithContext(ctx).Model(&dbmodel.Session{}).Select(constant.SessionExportBoundsSelect)
	if err := applyExportFilter(query, f).Scan(&bounds).Error; err != nil {
		return bounds, ierr.Wrap(ierr.ErrDBQuery, err, "get session export bounds")
	}
	return bounds, nil
}

// ListSessionsForExport 在固定上界内按主键取下一页，超大引用数组只返回标记。
func (r *sessionReadRepository) ListSessionsForExport(ctx context.Context, f session.ExportFilter, afterID, throughID uint) ([]*session.ExportSessionRow, error) {
	if f.OwnerIDs != nil && len(f.OwnerIDs) == 0 {
		return nil, nil
	}
	budget := constant.DatasetExportReferenceMaxBytes
	format := exportByteLengthFormat(r.db)
	size := fmt.Sprintf(constant.SessionExportReferenceSizeFormat,
		fmt.Sprintf(format, constant.FieldMessageIDs),
		fmt.Sprintf(format, constant.FieldToolIDs))
	query := r.db.WithContext(ctx).Model(&dbmodel.Session{}).
		Select(fmt.Sprintf(constant.SessionExportPageSelectFormat, size), budget, budget, budget)

	var rows []exportSessionRow
	err := applyExportFilter(query, f).
		Where(clause.Gt{Column: constant.FieldID, Value: afterID}).
		Where(clause.Lte{Column: constant.FieldID, Value: throughID}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: constant.FieldID}}).
		Limit(constant.DatasetExportPageSessions).
		Find(&rows).Error
	if err != nil {
		return nil, ierr.Wrap(ierr.ErrDBQuery, err, "page sessions for export")
	}
	return lo.Map(rows, toExportSessionRow), nil
}

// LoadSessionExportPayload 在同一事务快照中计量并加载一个会话的消息与工具。
func (r *sessionReadRepository) LoadSessionExportPayload(ctx context.Context, row *session.ExportSessionRow) (
	messages map[uint]*session.MessageDetailProjection, tools map[uint]*session.ToolDetailProjection, overBudget bool, err error,
) {
	if row.OverBudget || len(row.MessageIDs)+len(row.ToolIDs) > constant.DatasetExportMaxReferences {
		return nil, nil, true, nil
	}
	if len(row.MessageIDs) == 0 && len(row.ToolIDs) == 0 {
		return nil, nil, false, nil
	}

	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		messageIDs := lo.Uniq(row.MessageIDs)
		toolIDs := lo.Uniq(row.ToolIDs)
		messageBytes, sizeErr := exportPayloadBytes(tx.Model(&dbmodel.Message{}), constant.FieldMessage, messageIDs, row.MessageIDs)
		if sizeErr != nil {
			return sizeErr
		}
		toolBytes, sizeErr := exportPayloadBytes(tx.Model(&dbmodel.Tool{}), constant.FieldTool, toolIDs, row.ToolIDs)
		if sizeErr != nil {
			return sizeErr
		}
		if messageBytes+toolBytes > constant.DatasetExportPayloadMaxBytes {
			overBudget = true
			return nil
		}

		scoped := *r
		scoped.db = tx
		messageRows, loadErr := scoped.FindMessagesByIDs(ctx, messageIDs)
		if loadErr != nil {
			return loadErr
		}
		toolRows, loadErr := scoped.FindToolsByIDs(ctx, toolIDs)
		if loadErr != nil {
			return loadErr
		}
		messages = lo.SliceToMap(messageRows, func(m *session.MessageDetailProjection) (uint, *session.MessageDetailProjection) {
			return m.ID, m
		})
		tools = lo.SliceToMap(toolRows, func(t *session.ToolDetailProjection) (uint, *session.ToolDetailProjection) {
			return t.ID, t
		})
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, false, ierr.Wrap(ierr.ErrDBQuery, err, "load session export payload")
	}
	return messages, tools, overBudget, nil
}

type exportPayloadSize struct {
	ID        uint
	BodyBytes int64
}

func exportPayloadBytes(query *gorm.DB, field string, uniqueIDs, references []uint) (int64, error) {
	if len(uniqueIDs) == 0 {
		return 0, nil
	}
	var sizes []exportPayloadSize
	selectSQL := fmt.Sprintf(constant.SessionExportPayloadSizeSelectFormat, fmt.Sprintf(exportByteLengthFormat(query), field))
	err := query.Select(selectSQL).
		Where(fmt.Sprintf(constant.DBConditionInTemplate, constant.FieldID), uniqueIDs).
		Where(constant.DBConditionDeletedAtZero).
		Scan(&sizes).Error
	if err != nil {
		return 0, err
	}
	byID := lo.SliceToMap(sizes, func(row exportPayloadSize) (uint, int64) {
		return row.ID, row.BodyBytes
	})
	var total int64
	for _, id := range references {
		total += byID[id]
	}
	return total, nil
}

func exportByteLengthFormat(db *gorm.DB) string {
	if db.Name() == constant.DBDialectSQLite {
		return constant.SessionExportSQLiteByteLengthFormat
	}
	return constant.SessionExportPostgresByteLengthFormat
}
