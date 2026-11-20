package blobstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/samber/lo"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/common/ierr"
	"github.com/hcd233/aris-mass/internal/config"
	"github.com/hcd233/aris-mass/internal/domain/blob"
	"github.com/hcd233/aris-mass/internal/infrastructure/database/dao"
	dbmodel "github.com/hcd233/aris-mass/internal/infrastructure/database/model"
	"github.com/hcd233/aris-mass/internal/logger"
)

// CollectImageRefs 收集指定 message 中引用的图片 sha256（去重）
//
//	在硬删除 message 的同一事务内调用，供 SchedulePurge 持久化候选。
//
//	@param ctx context.Context
//	@param db *gorm.DB
//	@param messageIDs []uint
//	@return []string
//	@return error
//	@author centonhuang
//	@update 2026-11-06 10:00:00
func CollectImageRefs(ctx context.Context, db *gorm.DB, messageIDs []uint) ([]string, error) {
	db = db.WithContext(ctx)
	var shas []string
	for _, chunk := range lo.Chunk(messageIDs, max(config.SQLBatchSize, 1)) {
		var rows []messageRow
		if err := db.Table(constant.TableMessages).Select(constant.MessageRawFields).
			Where(constant.DBConditionIDInAndMessageLike, chunk, constant.BlobAnyRefLikePattern).
			Find(&rows).Error; err != nil {
			return nil, ierr.Wrap(ierr.ErrDBQuery, err, "collect image refs")
		}
		for _, row := range rows {
			shas = append(shas, parseImageRefs(ctx, row)...)
		}
	}
	return lo.Uniq(shas), nil
}

// SchedulePurge 在硬删消息的同一事务内持久化图片清理候选。
func SchedulePurge(tx *gorm.DB, shas []string, now time.Time) error {
	for _, chunk := range lo.Chunk(shas, max(config.SQLBatchSize, 1)) {
		if err := dao.GetBlobDAO().SchedulePurge(tx, chunk, now); err != nil {
			return ierr.Wrap(ierr.ErrDBUpdate, err, "schedule blob purge")
		}
	}
	return nil
}

// PurgeUnreferenced 分批复查到期候选的消息引用，并重试尚未完成的对象删除。
func PurgeUnreferenced(ctx context.Context, db *gorm.DB, store blob.ObjectStore, now time.Time) (int64, error) {
	db = db.WithContext(ctx)
	var afterID uint
	var count int64
	for {
		ids, err := dao.GetBlobDAO().ListDuePurgeIDs(db, now, afterID, constant.BlobPurgeBatchSize)
		if err != nil {
			return count, ierr.Wrap(ierr.ErrDBQuery, err, "list blob purge candidates")
		}
		if len(ids) == 0 {
			return count, nil
		}
		afterID = ids[len(ids)-1]
		deleting, err := claimPurgeBatch(db, ids, now)
		if err != nil {
			return count, ierr.Wrap(ierr.ErrDBUpdate, err, "claim blob purge candidates")
		}
		for _, id := range deleting {
			n, err := deleteClaimedBlob(ctx, db, store, id)
			if err != nil {
				return count, ierr.Wrap(ierr.ErrDBDelete, err, "delete claimed blob")
			}
			count += n
		}
	}
}

func claimPurgeBatch(db *gorm.DB, ids []uint, now time.Time) ([]uint, error) {
	var deleting []uint
	blobDAO := dao.GetBlobDAO()
	err := db.Transaction(func(tx *gorm.DB) error {
		rows, err := blobDAO.LockPurgeRows(tx, ids)
		if err != nil {
			return err
		}
		// 写入方持有 KEY SHARE 到消息提交。拿到排他锁后再查，才能看到先提交的引用。
		candidates := lo.FilterMap(rows, func(row dbmodel.Blob, _ int) (string, bool) {
			return row.SHA256, row.PurgeAt != nil && !row.PurgeAt.After(now) && row.Status != enum.BlobStatusDeleting
		})
		referenced, err := referencedSHAs(tx, candidates)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.PurgeAt == nil || row.PurgeAt.After(now) {
				continue
			}
			if referenced[row.SHA256] {
				if err := blobDAO.ClearPurge(tx, row.ID); err != nil {
					return err
				}
				continue
			}
			if row.Status != enum.BlobStatusDeleting {
				if err := blobDAO.MarkDeleting(tx, row); err != nil {
					return err
				}
			}
			deleting = append(deleting, row.ID)
		}
		return nil
	})
	return deleting, err
}

func deleteClaimedBlob(ctx context.Context, db *gorm.DB, store blob.ObjectStore, id uint) (int64, error) {
	var count int64
	blobDAO := dao.GetBlobDAO()
	err := db.Transaction(func(tx *gorm.DB) error {
		row, err := blobDAO.LockMetaByID(tx, id, constant.DBLockStrengthUpdate)
		if err != nil || row == nil || row.Status != enum.BlobStatusDeleting {
			return err
		}
		if row.ObjectMayExist && row.StorageKey != "" {
			if store == nil {
				return nil
			}
			deleteCtx, cancel := context.WithTimeout(ctx, constant.BlobDeleteTimeout)
			defer cancel()
			if err := store.Delete(deleteCtx, row.StorageKey); err != nil {
				logger.WithCtx(ctx).Error("[BlobPurge] Failed to delete object, blob purge remains scheduled", zap.String("key", row.StorageKey), zap.Error(err))
				return nil
			}
		}
		count, err = blobDAO.DeleteByIDs(tx, []uint{id})
		return err
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// referencedSHAs 全表扫描含图片引用的 message，返回 candidates 中仍出现在 message 文本里的 sha256
//
//	这里只做子串匹配不解析 JSON：多匹配只会多保留，解析失败的行也不会导致误删。
func referencedSHAs(db *gorm.DB, candidates []string) (map[string]bool, error) {
	referenced := map[string]bool{}
	if len(candidates) == 0 {
		return referenced, nil
	}
	predicates := make([]clause.Expression, 0, len(candidates))
	for _, sha := range candidates {
		predicates = append(predicates, clause.Like{
			Column: clause.Column{Name: constant.FieldMessage},
			Value:  fmt.Sprintf(constant.BlobSHA256LikePatternTemplate, sha),
		})
	}
	var batch []messageRow
	err := db.Table(constant.TableMessages).Select(constant.FieldID, constant.FieldMessage).
		Where(constant.DBConditionMessageLike, constant.BlobAnyRefLikePattern).
		Where(clause.Or(predicates...)).
		FindInBatches(&batch, max(config.SQLBatchSize, 1), func(_ *gorm.DB, _ int) error {
			for _, row := range batch {
				for _, sha := range candidates {
					if !referenced[sha] && strings.Contains(row.Message, sha) {
						referenced[sha] = true
					}
				}
			}
			return nil
		}).Error
	if err != nil {
		return nil, ierr.Wrap(ierr.ErrDBQuery, err, "scan referenced images")
	}
	return referenced, nil
}
