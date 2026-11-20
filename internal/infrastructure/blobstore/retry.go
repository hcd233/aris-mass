package blobstore

import (
	"context"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/common/ierr"
	"github.com/hcd233/aris-mass/internal/domain/blob"
	"github.com/hcd233/aris-mass/internal/infrastructure/database/dao"
	dbmodel "github.com/hcd233/aris-mass/internal/infrastructure/database/model"
	"github.com/hcd233/aris-mass/internal/logger"
)

// RetryStats 一次重试的统计：Uploaded 为本次成功数，Pending/Failed 为执行后的存量
//
//	@author centonhuang
//	@update 2026-11-05 10:00:00
type RetryStats struct{ Uploaded, Pending, Failed int64 }

// RetryUploads 补传一批到期的 pending 图片
//
//	先取 ID 再逐行读 data，避免一次把整批大字段读进内存；单行出错只影响该行。
//	pending 行可能是上传池来不及处理时直接落库的原图，所以上传前统一再压缩一次
//	（已是存储版本的字节压缩结果不变）。
//
//	@param ctx context.Context
//	@param db *gorm.DB
//	@param store blob.ObjectStore 为 nil 时直接返回
//	@param now time.Time
//	@return RetryStats
//	@return error
//	@author centonhuang
//	@update 2026-11-05 10:00:00
func RetryUploads(ctx context.Context, db *gorm.DB, store blob.ObjectStore, now time.Time) (RetryStats, error) {
	var stats RetryStats
	if store == nil {
		return stats, nil
	}
	db = db.WithContext(ctx)
	blobDAO := dao.GetBlobDAO()
	ids, err := blobDAO.ListDueRetryIDs(db, now, constant.BlobRetryBatchSize)
	if err != nil {
		return stats, ierr.Wrap(ierr.ErrDBQuery, err, "list due blob retries")
	}
	for _, id := range ids {
		status, processErr := processPending(ctx, db, store, id, now)
		if processErr != nil {
			logger.WithCtx(ctx).Error("[BlobRetry] Failed to process blob", zap.Uint("id", id), zap.Error(processErr))
			continue
		}
		if status == enum.BlobStatusUploaded {
			stats.Uploaded++
		}
	}
	if stats.Pending, err = blobDAO.CountByStatus(db, enum.BlobStatusPending); err != nil {
		return stats, ierr.Wrap(ierr.ErrDBQuery, err, "count pending blobs")
	}
	if stats.Failed, err = blobDAO.CountByStatus(db, enum.BlobStatusFailed); err != nil {
		return stats, ierr.Wrap(ierr.ErrDBQuery, err, "count failed blobs")
	}
	return stats, nil
}

// processPending 处理新上传或到期重试，原图已在数据库中。
func processPending(ctx context.Context, db *gorm.DB, store blob.ObjectStore, id uint, now time.Time) (enum.BlobStatus, error) {
	blobDAO := dao.GetBlobDAO()
	row, err := blobDAO.GetByID(db, id)
	if err != nil {
		return enum.BlobStatusPending, ierr.Wrap(ierr.ErrDBQuery, err, "load pending blob")
	}
	if row == nil || row.Status != enum.BlobStatusPending || row.NextRetryAt == nil || row.NextRetryAt.After(now) {
		return enum.BlobStatusPending, nil
	}
	res := Compress(row.Data)
	if store != nil && len(row.Data) > 0 && !res.Unprocessable {
		// PUT 成功但状态写入失败时，清理任务仍能找到实际对象。
		key := StorageKey(row.SHA256, res.Mime)
		if key != row.StorageKey || !row.ObjectMayExist {
			if err := blobDAO.ReserveUploadKey(db, id, row.StorageKey, key); err != nil {
				return enum.BlobStatusPending, ierr.Wrap(ierr.ErrDBUpdate, err, "reserve blob storage key")
			}
		}
	}
	status := enum.BlobStatusPending
	err = db.Transaction(func(tx *gorm.DB) error {
		// 允许消息持 KEY SHARE 同时写入，阻止重试与删除同一对象并行。
		current, lockErr := blobDAO.LockMetaByID(tx, id, constant.DBLockStrengthNoKeyUpdate)
		if lockErr != nil {
			return lockErr
		}
		if current == nil || current.Status != enum.BlobStatusPending || !current.UpdatedAt.Equal(row.UpdatedAt) {
			return nil
		}
		status, lockErr = finishUpload(ctx, tx, store, current, res, now)
		return lockErr
	})
	if err != nil {
		return enum.BlobStatusPending, ierr.Wrap(ierr.ErrDBUpdate, err, "finish blob upload")
	}
	return status, nil
}

func finishUpload(ctx context.Context, tx *gorm.DB, store blob.ObjectStore, row *dbmodel.Blob, res CompressResult, now time.Time) (enum.BlobStatus, error) {
	blobDAO := dao.GetBlobDAO()
	if len(res.Data) == 0 {
		return recordFailure(ctx, tx, row, ierr.New(ierr.ErrInternal, "blob data missing"), now)
	}
	if res.Unprocessable {
		logger.WithCtx(ctx).Warn("[BlobRetry] Blob cannot be compressed under limit, marked unprocessable", zap.Uint("id", row.ID))
		return enum.BlobStatusUnprocessable, blobDAO.MarkUnprocessable(tx, row.ID)
	}
	meta := dao.BlobUploadedMeta{
		StorageKey: row.StorageKey,
		Mime:       res.Mime,
		Size:       int64(len(res.Data)),
		Width:      res.Width,
		Height:     res.Height,
	}
	if store == nil {
		return enum.BlobStatusPending, blobDAO.SavePendingVersion(tx, row.ID, meta, res.Data)
	}
	if !row.ObjectMayExist || row.StorageKey == "" {
		return enum.BlobStatusPending, ierr.New(ierr.ErrInternal, "blob upload intent missing")
	}
	putCtx, cancel := context.WithTimeout(ctx, constant.BlobPutTimeout)
	defer cancel()
	if err := store.Put(putCtx, row.StorageKey, res.Mime, res.Data); err != nil {
		if saveErr := blobDAO.SavePendingVersion(tx, row.ID, meta, res.Data); saveErr != nil {
			return enum.BlobStatusPending, saveErr
		}
		return recordFailure(ctx, tx, row, err, now)
	}
	return enum.BlobStatusUploaded, blobDAO.MarkUploaded(tx, row.ID, meta)
}

// recordFailure 按退避推后重试；达到上限转 failed
func recordFailure(ctx context.Context, db *gorm.DB, row *dbmodel.Blob, cause error, now time.Time) (enum.BlobStatus, error) {
	log := logger.WithCtx(ctx)
	attempts := row.Attempts + 1
	lastError := TruncateError(cause)
	if attempts >= constant.BlobRetryMaxAttempts {
		log.Error("[BlobRetry] Blob upload exhausted retries", zap.Uint("id", row.ID), zap.Int("attempts", attempts), zap.Error(cause))
		return enum.BlobStatusFailed, dao.GetBlobDAO().MarkFailed(db, row.ID, attempts, lastError)
	}
	backoff := constant.BlobRetryBackoff[min(attempts-1, len(constant.BlobRetryBackoff)-1)]
	log.Warn("[BlobRetry] Blob upload failed, will retry", zap.Uint("id", row.ID), zap.Int("attempts", attempts), zap.Error(cause))
	return enum.BlobStatusPending, dao.GetBlobDAO().MarkRetry(db, row.ID, attempts, lastError, now.Add(backoff))
}
