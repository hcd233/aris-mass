package blobstore

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/alitto/pond/v2"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/common/ierr"
	"github.com/hcd233/aris-mass/internal/domain/blob"
	"github.com/hcd233/aris-mass/internal/infrastructure/database/dao"
	dbmodel "github.com/hcd233/aris-mass/internal/infrastructure/database/model"
	"github.com/hcd233/aris-mass/internal/logger"
	"github.com/hcd233/aris-mass/internal/util"
)

// Uploader 先持久化原图，再由独立协程池或同步迁移命令压缩和上传。
type Uploader struct {
	db       *gorm.DB
	store    blob.ObjectStore
	pool     pond.Pool
	draining atomic.Bool
}

// NewAsyncUploader 构造服务进程使用的异步上传器。
func NewAsyncUploader(db *gorm.DB, store blob.ObjectStore, workers, queueSize int) *Uploader {
	return &Uploader{db: db, store: store, pool: pond.NewPool(workers, pond.WithQueueSize(queueSize))}
}

// NewSyncUploader 构造 CLI 迁移使用的同步上传器。
func NewSyncUploader(db *gorm.DB, store blob.ObjectStore) *Uploader {
	return &Uploader{db: db, store: store}
}

// Submit 持久化原图。返回错误时，调用方保留 data URI。
func (u *Uploader) Submit(ctx context.Context, sha string, raw []byte, stats *ExternalizeStats) error {
	db := u.db.WithContext(ctx)
	now := time.Now().UTC()
	purgeAt := now.Add(constant.BlobPurgeGrace)
	row := &dbmodel.Blob{
		SHA256:       sha,
		Status:       enum.BlobStatusPending,
		Data:         raw,
		Mime:         detectMime(raw),
		Size:         int64(len(raw)),
		OriginalSize: int64(len(raw)),
		NextRetryAt:  &now,
		PurgeAt:      &purgeAt,
	}
	blobDAO := dao.GetBlobDAO()
	inserted, err := blobDAO.InsertIgnoreConflict(db, row)
	if err != nil {
		return ierr.Wrap(ierr.ErrDBCreate, err, "insert pending blob")
	}
	if !inserted {
		rows, findErr := blobDAO.FindMetaBySHA256s(db, []string{sha})
		if findErr != nil {
			return ierr.Wrap(ierr.ErrDBQuery, findErr, "find conflicting blob")
		}
		if len(rows) == 0 || rows[0].Status == enum.BlobStatusDeleting {
			return ierr.New(ierr.ErrDataNotExists, "blob being deleted")
		}
		stats.Reused++
		return nil
	}
	if u.pool == nil {
		status, processErr := processPending(ctx, db, u.store, row.ID, now)
		if processErr != nil {
			logger.WithCtx(ctx).Error("[BlobUploader] Initial upload failed, pending blob kept", zap.Uint("id", row.ID), zap.Error(processErr))
		}
		stats.count(status)
		return nil
	}
	if !u.draining.Load() {
		taskCtx := util.CopyContextValues(ctx)
		if _, ok := u.pool.TrySubmit(func() { u.run(taskCtx, row.ID) }); ok {
			stats.Queued++
			return nil
		}
	}
	stats.Pending++
	return nil
}

// Stop 等待正在执行的上传。队列中剩余图片已经持久化，由重试任务接手。
func (u *Uploader) Stop() {
	u.draining.Store(true)
	if u.pool != nil {
		u.pool.StopAndWait()
	}
}

func (u *Uploader) run(ctx context.Context, id uint) {
	if u.draining.Load() {
		return
	}
	if _, err := processPending(ctx, u.db.WithContext(ctx), u.store, id, time.Now().UTC()); err != nil {
		logger.WithCtx(ctx).Error("[BlobUploader] Initial upload failed, pending blob kept", zap.Uint("id", id), zap.Error(err))
	}
}
