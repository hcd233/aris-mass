package blobstore

import (
	"context"
	"slices"
	"strings"
	"unicode/utf8"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/common/ierr"
	"github.com/hcd233/aris-mass/internal/common/vo"
	"github.com/hcd233/aris-mass/internal/config"
	"github.com/hcd233/aris-mass/internal/infrastructure/database/dao"
	"github.com/hcd233/aris-mass/internal/logger"
)

// ExternalizeStats 一次外置的统计
//
//	Queued 为原图已落库、且交给上传池的新图；其余状态为提交时的处理结果。
//
//	@author centonhuang
//	@update 2026-11-06 10:00:00
type ExternalizeStats struct {
	Queued, Uploaded, Pending, Unprocessable, Reused, Failed int
	// SavedBytes message 中被移走的 data URI 字节数
	SavedBytes int64
}

func (s *ExternalizeStats) count(status enum.BlobStatus) {
	switch status {
	case enum.BlobStatusUploaded:
		s.Uploaded++
	case enum.BlobStatusUnprocessable:
		s.Unprocessable++
	default:
		s.Pending++
	}
}

func (s *ExternalizeStats) add(o ExternalizeStats) {
	s.Queued += o.Queued
	s.Uploaded += o.Uploaded
	s.Pending += o.Pending
	s.Unprocessable += o.Unprocessable
	s.Reused += o.Reused
	s.Failed += o.Failed
	s.SavedBytes += o.SavedBytes
}

// Externalizer 把 message 中的 base64 图片替换为引用，图片本体交给 Uploader
//
//	@author centonhuang
//	@update 2026-11-06 10:00:00
type Externalizer struct {
	db       *gorm.DB
	uploader *Uploader
}

// NewExternalizer 构造图片外置器
//
//	@param db *gorm.DB
//	@param uploader *Uploader
//	@return *Externalizer
//	@author centonhuang
//	@update 2026-11-06 10:00:00
func NewExternalizer(db *gorm.DB, uploader *Uploader) *Externalizer {
	return &Externalizer{db: db, uploader: uploader}
}

// Stop 停止上传池，队列中剩余图片由重试任务处理
//
//	@receiver e *Externalizer
//	@author centonhuang
//	@update 2026-11-06 10:00:00
func (e *Externalizer) Stop() {
	e.uploader.Stop()
}

// imageMeta 引用上附带的展示元数据
type imageMeta struct {
	mime string
	size int64
}

// Externalize 外置 msgs 中的 data URI 图片
//
//	返回新切片：有图片被替换的消息是副本，其余保持原指针；不修改入参。
//	已在 blobs 表的图复用其存储版本元数据；新图的引用带原图的 mime/size，本体交给上传器。
//	查询 blobs 表失败或上传器拒收的图片保留 data URI（checksum 与引用形态一致），之后由存量迁移补外置。
//
//	@receiver e *Externalizer
//	@param ctx context.Context
//	@param msgs []*vo.UnifiedMessage
//	@return []*vo.UnifiedMessage
//	@return ExternalizeStats
//	@author centonhuang
//	@update 2026-11-06 10:00:00
func (e *Externalizer) Externalize(ctx context.Context, msgs []*vo.UnifiedMessage) ([]*vo.UnifiedMessage, ExternalizeStats) {
	var stats ExternalizeStats
	sites, raws, order := collect(ctx, msgs, &stats)
	if len(order) == 0 {
		return msgs, stats
	}

	rows, err := dao.GetBlobDAO().FindMetaBySHA256s(e.db.WithContext(ctx), order)
	if err != nil {
		logger.WithCtx(ctx).Error("[BlobExternalizer] Failed to load blob meta", zap.Error(err))
		stats.Failed += len(order)
		return msgs, stats
	}
	refs := make(map[string]imageMeta, len(order))
	unavailable := make(map[string]bool)
	for _, row := range rows {
		if row.Status == enum.BlobStatusDeleting {
			unavailable[row.SHA256] = true
			continue
		}
		refs[row.SHA256] = imageMeta{mime: row.Mime, size: row.Size}
	}
	stats.Reused = len(refs)

	for _, sha := range order {
		if _, ok := refs[sha]; ok {
			continue
		}
		if unavailable[sha] {
			stats.Failed++
			continue
		}
		raw := raws[sha]
		if err := e.uploader.Submit(ctx, sha, raw, &stats); err != nil {
			logger.WithCtx(ctx).Error("[BlobExternalizer] Failed to submit image, kept data uri", zap.String("sha256", sha), zap.Error(err))
			stats.Failed++
			continue
		}
		refs[sha] = imageMeta{mime: detectMime(raw), size: int64(len(raw))}
	}

	out := vo.RewriteImageParts(msgs, func(mi, pi int, p *vo.UnifiedContentPart) *vo.UnifiedContentPart {
		sha, ok := sites[[2]int{mi, pi}]
		if !ok {
			return nil
		}
		meta, ok := refs[sha]
		if !ok {
			return nil
		}
		stats.SavedBytes += int64(len(p.ImageURL))
		cp := *p
		cp.ImageURL, cp.ImageMime, cp.ImageSize = vo.ImageRefOf(sha), meta.mime, meta.size
		return &cp
	})
	return out, stats
}

// LockAvailableImageRefs 锁定新引用直到消息写入提交，已被 GC 认领的图片恢复 data URI。
func LockAvailableImageRefs(tx *gorm.DB, original, rewritten []*vo.UnifiedMessage) ([]*vo.UnifiedMessage, error) {
	var shas []string
	for mi, msg := range rewritten {
		if msg == original[mi] {
			continue
		}
		for pi, part := range msg.Content.Parts {
			if part == nil || part == original[mi].Content.Parts[pi] {
				continue
			}
			if sha, ok := vo.ParseImageRef(part.ImageURL); ok {
				shas = append(shas, sha)
			}
		}
	}
	if len(shas) == 0 {
		return rewritten, nil
	}
	slices.Sort(shas)
	shas = slices.Compact(shas)
	available := make(map[string]bool, len(shas))
	for chunk := range slices.Chunk(shas, max(config.SQLBatchSize, 1)) {
		locked, err := dao.GetBlobDAO().LockReferenceSHAs(tx, chunk)
		if err != nil {
			return nil, ierr.Wrap(ierr.ErrDBQuery, err, "lock available image references")
		}
		for _, sha := range locked {
			available[sha] = true
		}
	}
	if len(available) == 0 {
		return original, nil
	}
	return vo.RewriteImageParts(rewritten, func(mi, pi int, part *vo.UnifiedContentPart) *vo.UnifiedContentPart {
		before := original[mi].Content.Parts[pi]
		if part != before {
			if sha, ok := vo.ParseImageRef(part.ImageURL); ok && !available[sha] {
				return before
			}
		}
		return nil
	}), nil
}

// collect 找出所有 data URI 图片：sites 为 (消息下标, 部分下标) → sha256，order 为去重后的首次出现顺序
func collect(ctx context.Context, msgs []*vo.UnifiedMessage, stats *ExternalizeStats) (sites map[[2]int]string, raws map[string][]byte, order []string) {
	sites, raws = map[[2]int]string{}, map[string][]byte{}
	for mi, m := range msgs {
		if m == nil || m.Content == nil {
			continue
		}
		for pi, p := range m.Content.Parts {
			if p == nil || p.Type != enum.ContentPartTypeImageURL || !strings.HasPrefix(p.ImageURL, constant.DataURLPrefix) {
				continue
			}
			_, raw, ok := vo.ParseImageDataURI(p.ImageURL)
			if !ok {
				logger.WithCtx(ctx).Warn("[BlobExternalizer] Invalid image data uri, kept as is", zap.Int("length", len(p.ImageURL)))
				stats.Failed++
				continue
			}
			sha := vo.SHA256Hex(raw)
			if _, seen := raws[sha]; !seen {
				raws[sha] = raw
				order = append(order, sha)
			}
			sites[[2]int{mi, pi}] = sha
		}
	}
	return sites, raws, order
}

// TruncateError 错误信息按字符截断到 last_error 列长度（varchar 按字符计，按字节截断会切坏 UTF-8）
//
//	@param err error
//	@return string
//	@author centonhuang
//	@update 2026-11-06 10:00:00
func TruncateError(err error) string {
	msg := strings.ToValidUTF8(err.Error(), "")
	if utf8.RuneCountInString(msg) <= constant.BlobLastErrorMaxLen {
		return msg
	}
	return string([]rune(msg)[:constant.BlobLastErrorMaxLen])
}
