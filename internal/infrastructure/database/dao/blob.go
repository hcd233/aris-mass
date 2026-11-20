package dao

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/enum"
	dbmodel "github.com/hcd233/aris-mass/internal/infrastructure/database/model"
)

// BlobDAO 消息图片 blobs 表数据访问对象
//
//	blobs 不软删，不复用 baseDAO（其查询恒带 deleted_at = 0）。
//
//	@author centonhuang
//	@update 2026-11-05 10:00:00
type BlobDAO struct{}

var blobDAOSingleton = &BlobDAO{}

// GetBlobDAO 获取 Blob DAO
//
//	@return *BlobDAO
//	@author centonhuang
//	@update 2026-11-05 10:00:00
func GetBlobDAO() *BlobDAO {
	return blobDAOSingleton
}

// FindMetaBySHA256s 批量查询元数据（不读 data）
func (dao *BlobDAO) FindMetaBySHA256s(db *gorm.DB, shas []string) ([]*dbmodel.Blob, error) {
	var rows []*dbmodel.Blob
	if len(shas) == 0 {
		return rows, nil
	}
	err := db.Select(constant.BlobMetaFields).
		Where(fmt.Sprintf(constant.DBConditionInTemplate, constant.FieldSHA256), shas).
		Find(&rows).Error
	return rows, err
}

// InsertIgnoreConflict 插入；sha256 已存在时返回 false（保留先到者）
func (dao *BlobDAO) InsertIgnoreConflict(db *gorm.DB, blob *dbmodel.Blob) (bool, error) {
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: constant.FieldSHA256}},
		DoNothing: true,
	}).Create(blob)
	return result.RowsAffected > 0, result.Error
}

// LockReferenceSHAs 锁定可引用的图片，直到消息写入事务结束。
func (dao *BlobDAO) LockReferenceSHAs(tx *gorm.DB, shas []string) ([]string, error) {
	var found []string
	err := tx.Model(&dbmodel.Blob{}).
		Where(fmt.Sprintf(constant.DBConditionInTemplate, constant.FieldSHA256), shas).
		Where(constant.DBConditionBlobStatusNotEquals, enum.BlobStatusDeleting).
		Order(constant.FieldSHA256).
		Clauses(clause.Locking{Strength: constant.DBLockStrengthKeyShare}).
		Pluck(constant.FieldSHA256, &found).Error
	return found, err
}

// ReserveUploadKey 在 PUT 前保存对象 key 和上传意图，供上传中断后的清理使用。
func (dao *BlobDAO) ReserveUploadKey(db *gorm.DB, id uint, previous, key string) error {
	return db.Model(&dbmodel.Blob{}).Where(constant.DBConditionBlobUploadKeyEquals, id, enum.BlobStatusPending, previous).
		UpdateColumns(map[string]any{
			constant.FieldStorageKey:         key,
			constant.FieldBlobObjectMayExist: true,
		}).Error
}

// LockMetaByID 锁定单条图片元数据，避免查询大字段。
func (dao *BlobDAO) LockMetaByID(tx *gorm.DB, id uint, strength string) (*dbmodel.Blob, error) {
	return firstOrNil(tx.Omit(constant.FieldBlobData).
		Where(constant.DBConditionWhereIDIn, []uint{id}).
		Clauses(clause.Locking{Strength: strength}))
}

// SchedulePurge 为尚未排期的图片记录清理时间。
func (dao *BlobDAO) SchedulePurge(db *gorm.DB, shas []string, now time.Time) error {
	candidates := db.Model(&dbmodel.Blob{}).
		Where(fmt.Sprintf(constant.DBConditionInTemplate, constant.FieldSHA256), shas).
		Where(constant.DBConditionBlobPurgeUnscheduled).
		Session(&gorm.Session{})
	if err := candidates.Where(constant.WhereCreatedAtLTE, now.Add(-constant.BlobPurgeGrace)).
		UpdateColumn(constant.FieldBlobPurgeAt, now).Error; err != nil {
		return err
	}
	return candidates.UpdateColumn(constant.FieldBlobPurgeAt, now.Add(constant.BlobPurgeGrace)).Error
}

// ListDuePurgeIDs 按 ID 分批读取到期的清理候选（含对象删除失败的行）。
func (dao *BlobDAO) ListDuePurgeIDs(db *gorm.DB, now time.Time, afterID uint, limit int) ([]uint, error) {
	var ids []uint
	err := db.Model(&dbmodel.Blob{}).Where(constant.DBConditionBlobPurgeDue, now, afterID).
		Order(constant.FieldID).Limit(limit).Pluck(constant.FieldID, &ids).Error
	return ids, err
}

// LockPurgeRows 锁定一批清理候选，直到确认引用并更新状态。
func (dao *BlobDAO) LockPurgeRows(tx *gorm.DB, ids []uint) ([]dbmodel.Blob, error) {
	var rows []dbmodel.Blob
	err := tx.Omit(constant.FieldBlobData).Where(constant.DBConditionWhereIDIn, ids).
		Order(constant.FieldSHA256).Clauses(clause.Locking{Strength: constant.DBLockStrengthUpdate}).
		Find(&rows).Error
	return rows, err
}

// ClearPurge 保留仍被引用的图片，清除本轮排期。
func (dao *BlobDAO) ClearPurge(tx *gorm.DB, id uint) error {
	return tx.Model(&dbmodel.Blob{ID: id}).UpdateColumn(constant.FieldBlobPurgeAt, nil).Error
}

// MarkDeleting 持久记录删除意图，再由对象清理阶段执行 DELETE。
func (dao *BlobDAO) MarkDeleting(tx *gorm.DB, row dbmodel.Blob) error {
	return tx.Model(&dbmodel.Blob{ID: row.ID}).UpdateColumns(map[string]any{
		constant.FieldStatus:             enum.BlobStatusDeleting,
		constant.FieldBlobObjectMayExist: row.ObjectMayExist || row.Status == enum.BlobStatusUploaded,
	}).Error
}

// GetBySHA256 按 sha256 查询完整行；不存在返回 nil, nil
func (dao *BlobDAO) GetBySHA256(db *gorm.DB, sha string) (*dbmodel.Blob, error) {
	return firstOrNil(db.Where(&dbmodel.Blob{SHA256: sha}))
}

// GetPreviewBySHA256 只读取可内联预览的大小范围内的 data。
func (dao *BlobDAO) GetPreviewBySHA256(db *gorm.DB, sha string) (*dbmodel.Blob, error) {
	return firstOrNil(db.Select(constant.DBSelectBlobPreview, constant.BlobMaxStoredBytes).
		Where(&dbmodel.Blob{SHA256: sha}))
}

// GetByID 按 ID 查询完整行；不存在返回 nil, nil
func (dao *BlobDAO) GetByID(db *gorm.DB, id uint) (*dbmodel.Blob, error) {
	return firstOrNil(db.Where(constant.DBConditionWhereIDIn, []uint{id}))
}

// ListDueRetryIDs 到期待重试的 pending 行 ID，按 next_retry_at 升序
func (dao *BlobDAO) ListDueRetryIDs(db *gorm.DB, now time.Time, limit int) ([]uint, error) {
	var ids []uint
	err := db.Model(&dbmodel.Blob{}).
		Where(constant.DBConditionBlobDue, enum.BlobStatusPending, now).
		Order(constant.DBOrderNextRetryAtAsc).
		Limit(limit).
		Pluck(constant.FieldID, &ids).Error
	return ids, err
}

// BlobUploadedMeta 上传成功时写回的存储版本元数据
//
//	@author centonhuang
//	@update 2026-11-06 10:00:00
type BlobUploadedMeta struct {
	StorageKey, Mime string
	Size             int64
	Width, Height    int
}

// SavePendingVersion 暂存压缩后的存储版本，继续等待对象上传。
func (dao *BlobDAO) SavePendingVersion(db *gorm.DB, id uint, meta BlobUploadedMeta, data []byte) error {
	return retryableRow(db, id).Updates(map[string]any{
		constant.FieldBlobData:  data,
		constant.FieldMime:      meta.Mime,
		constant.FieldSize:      meta.Size,
		constant.FieldWidth:     meta.Width,
		constant.FieldHeight:    meta.Height,
		constant.FieldUpdatedAt: time.Now().UTC(),
	}).Error
}

// MarkUploaded 上传成功：状态改为 uploaded、清空 data 并写回存储版本元数据（只作用于 pending/failed）
//
//	@author centonhuang
//	@update 2026-11-06 10:00:00
func (dao *BlobDAO) MarkUploaded(db *gorm.DB, id uint, meta BlobUploadedMeta) error {
	return retryableRow(db, id).Updates(map[string]any{
		constant.FieldStatus:     enum.BlobStatusUploaded,
		constant.FieldBlobData:   nil,
		constant.FieldStorageKey: meta.StorageKey,
		constant.FieldMime:       meta.Mime,
		constant.FieldSize:       meta.Size,
		constant.FieldWidth:      meta.Width,
		constant.FieldHeight:     meta.Height,
		constant.FieldUpdatedAt:  time.Now().UTC(),
	}).Error
}

// MarkUnprocessable 补传时发现无法压缩到上限内：转 unprocessable，data 保留，不再重试
//
//	@author centonhuang
//	@update 2026-11-06 10:00:00
func (dao *BlobDAO) MarkUnprocessable(db *gorm.DB, id uint) error {
	return retryableRow(db, id).Updates(map[string]any{
		constant.FieldStatus:    enum.BlobStatusUnprocessable,
		constant.FieldUpdatedAt: time.Now().UTC(),
	}).Error
}

// DeleteByIDs 硬删除指定行
//
//	@author centonhuang
//	@update 2026-11-06 10:00:00
func (dao *BlobDAO) DeleteByIDs(db *gorm.DB, ids []uint) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	result := db.Where(constant.DBConditionWhereIDIn, ids).Delete(&dbmodel.Blob{})
	return result.RowsAffected, result.Error
}

// MarkRetry 上传失败、尚未到上限：记录尝试次数与原因，推后下次重试
func (dao *BlobDAO) MarkRetry(db *gorm.DB, id uint, attempts int, lastError string, next time.Time) error {
	return retryableRow(db, id).Updates(map[string]any{
		constant.FieldStatus:      enum.BlobStatusPending,
		constant.FieldAttempts:    attempts,
		constant.FieldLastError:   lastError,
		constant.FieldNextRetryAt: next,
		constant.FieldUpdatedAt:   time.Now().UTC(),
	}).Error
}

// MarkFailed 超过重试上限：转 failed，data 保留
func (dao *BlobDAO) MarkFailed(db *gorm.DB, id uint, attempts int, lastError string) error {
	return retryableRow(db, id).Updates(map[string]any{
		constant.FieldStatus:    enum.BlobStatusFailed,
		constant.FieldAttempts:  attempts,
		constant.FieldLastError: lastError,
		constant.FieldUpdatedAt: time.Now().UTC(),
	}).Error
}

// ResetFailed 把全部 failed 行重置为立即可重试的 pending
func (dao *BlobDAO) ResetFailed(db *gorm.DB, now time.Time) (int64, error) {
	result := db.Model(&dbmodel.Blob{}).
		Where(constant.DBConditionBlobStatusEquals, enum.BlobStatusFailed).
		Updates(map[string]any{
			constant.FieldStatus:      enum.BlobStatusPending,
			constant.FieldAttempts:    0,
			constant.FieldNextRetryAt: now,
			constant.FieldUpdatedAt:   time.Now().UTC(),
		})
	return result.RowsAffected, result.Error
}

// CountByStatus 统计指定状态的行数
func (dao *BlobDAO) CountByStatus(db *gorm.DB, status enum.BlobStatus) (int64, error) {
	var count int64
	err := db.Model(&dbmodel.Blob{}).Where(constant.DBConditionBlobStatusEquals, status).Count(&count).Error
	return count, err
}

// retryableRow 只命中 pending/failed 行，保证 uploaded 不可被覆盖
func retryableRow(db *gorm.DB, id uint) *gorm.DB {
	return db.Model(&dbmodel.Blob{}).Where(constant.DBConditionBlobRetryable, id, constant.BlobRetryableStatuses)
}

func firstOrNil(query *gorm.DB) (*dbmodel.Blob, error) {
	var row dbmodel.Blob
	err := query.First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}
