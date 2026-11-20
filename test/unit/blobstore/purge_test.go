package blobstore

import (
	"slices"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/enum"
	"github.com/hcd233/aris-mass/internal/common/ierr"
	"github.com/hcd233/aris-mass/internal/common/vo"
	"github.com/hcd233/aris-mass/internal/infrastructure/blobstore"
	"github.com/hcd233/aris-mass/internal/infrastructure/database/dao"
	dbmodel "github.com/hcd233/aris-mass/internal/infrastructure/database/model"
)

type purgeEnv struct {
	db               *gorm.DB
	store            *fakeStore
	doomed, survivor *dbmodel.Message
	// a 只被 doomed 引用（已上传）；b 被两条消息引用；c 只被 doomed 引用（pending）
	a, b, c *dbmodel.Blob
}

func newPurgeEnv(t *testing.T) *purgeEnv {
	t.Helper()
	db := openBlobDB(t)
	if err := db.AutoMigrate(&dbmodel.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	e := &purgeEnv{db: db, store: newFakeStore()}
	e.a = insertBlob(t, db, &dbmodel.Blob{SHA256: shaOf('a'), Status: enum.BlobStatusUploaded, StorageKey: "images/a.png", Mime: "image/png"})
	e.b = insertBlob(t, db, &dbmodel.Blob{SHA256: shaOf('b'), Status: enum.BlobStatusUploaded, StorageKey: "images/b.png", Mime: "image/png"})
	e.c = insertBlob(t, db, pendingBlob(shaOf('c'), e.a.CreatedAt))
	e.store.objects["images/a.png"] = []byte("a")
	e.store.objects["images/b.png"] = []byte("b")

	e.doomed = &dbmodel.Message{CheckSum: "doomed", Message: imageMsg(vo.ImageRefOf(e.a.SHA256), vo.ImageRefOf(e.b.SHA256), vo.ImageRefOf(e.c.SHA256))}
	e.survivor = &dbmodel.Message{CheckSum: "survivor", Message: imageMsg(vo.ImageRefOf(e.b.SHA256))}
	for _, m := range []*dbmodel.Message{e.doomed, e.survivor} {
		if err := db.Create(m).Error; err != nil {
			t.Fatalf("create message: %v", err)
		}
	}
	return e
}

// collectAndDeleteDoomed 按软删清理 cron 的顺序：在同一事务排期并硬删消息。
func (e *purgeEnv) collectAndDeleteDoomed(t *testing.T) time.Time {
	t.Helper()
	now := time.Now().UTC()
	if err := e.db.Transaction(func(tx *gorm.DB) error {
		shas, err := blobstore.CollectImageRefs(t.Context(), tx, []uint{e.doomed.ID})
		if err != nil {
			return err
		}
		slices.Sort(shas)
		if !slices.Equal(shas, []string{e.a.SHA256, e.b.SHA256, e.c.SHA256}) {
			t.Errorf("collected=%v", shas)
		}
		if err := blobstore.SchedulePurge(tx, shas, now); err != nil {
			return err
		}
		_, err = dao.GetMessageDAO().HardDeleteByIDs(tx, []uint{e.doomed.ID})
		return err
	}); err != nil {
		t.Fatalf("schedule blob purge and delete message: %v", err)
	}
	return now.Add(constant.BlobPurgeGrace)
}

func (e *purgeEnv) exists(t *testing.T, b *dbmodel.Blob) bool {
	t.Helper()
	row, err := dao.GetBlobDAO().GetBySHA256(e.db, b.SHA256)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return row != nil
}

// TestDeleteUnreferencedKeepsSharedImages 只删不再被引用的图片；已上传的同时删对象
func TestDeleteUnreferencedKeepsSharedImages(t *testing.T) {
	t.Parallel()
	e := newPurgeEnv(t)
	due := e.collectAndDeleteDoomed(t)

	n, err := blobstore.PurgeUnreferenced(t.Context(), e.db, e.store, due.Add(-time.Minute))
	if err != nil || n != 0 || !e.exists(t, e.a) || !e.exists(t, e.c) {
		t.Fatalf("during grace: n=%d err=%v", n, err)
	}
	n, err = blobstore.PurgeUnreferenced(t.Context(), e.db, e.store, due)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if e.exists(t, e.a) || e.exists(t, e.c) || !e.exists(t, e.b) {
		t.Fatalf("a=%v b=%v c=%v", e.exists(t, e.a), e.exists(t, e.b), e.exists(t, e.c))
	}
	if !slices.Equal(e.store.deletes, []string{"images/a.png"}) || e.store.objects["images/b.png"] == nil {
		t.Fatalf("deletes=%v", e.store.deletes)
	}
}

// TestDeleteUnreferencedKeepsRowWhenObjectDeleteFails 对象删除失败时保留行，避免留下无从追踪的孤儿对象
func TestDeleteUnreferencedKeepsRowWhenObjectDeleteFails(t *testing.T) {
	t.Parallel()
	e := newPurgeEnv(t)
	e.store.failDelete = true
	due := e.collectAndDeleteDoomed(t)

	n, err := blobstore.PurgeUnreferenced(t.Context(), e.db, e.store, due)
	if err != nil || n != 1 || !e.exists(t, e.a) || e.exists(t, e.c) {
		t.Fatalf("n=%d err=%v a=%v c=%v", n, err, e.exists(t, e.a), e.exists(t, e.c))
	}
	e.store.failDelete = false
	n, err = blobstore.PurgeUnreferenced(t.Context(), e.db, e.store, due.Add(time.Hour))
	if err != nil || n != 1 || e.exists(t, e.a) || !e.exists(t, e.b) {
		t.Fatalf("retry without message candidates: n=%d err=%v a=%v b=%v", n, err, e.exists(t, e.a), e.exists(t, e.b))
	}
}

// TestDeleteUnreferencedWithoutStoreKeepsUploaded 对象存储未配置时 uploaded 行保留
func TestDeleteUnreferencedWithoutStoreKeepsUploaded(t *testing.T) {
	t.Parallel()
	e := newPurgeEnv(t)
	due := e.collectAndDeleteDoomed(t)

	n, err := blobstore.PurgeUnreferenced(t.Context(), e.db, nil, due)
	if err != nil || n != 1 || !e.exists(t, e.a) || e.exists(t, e.c) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestPurgeRetryRejectsReferenceToClaimedBlob(t *testing.T) {
	t.Parallel()
	e := newPurgeEnv(t)
	raw := smallPNG(t, 23)
	original := []*vo.UnifiedMessage{imageMsg(dataURI(raw))}
	ext := blobstore.NewExternalizer(e.db, blobstore.NewSyncUploader(e.db, e.store))
	rewritten, _ := ext.Externalize(t.Context(), original)
	if rewritten[0] == original[0] {
		t.Fatal("image was not externalized")
	}
	sha := vo.SHA256Hex(raw)
	now := time.Now().UTC().Add(2 * constant.BlobPurgeGrace)
	e.store.failDelete = true
	if n, err := blobstore.PurgeUnreferenced(t.Context(), e.db, e.store, now); n != 0 || err != nil {
		t.Fatalf("purge n=%d err=%v", n, err)
	}
	row, err := dao.GetBlobDAO().GetBySHA256(e.db, sha)
	if err != nil || row == nil || row.Status != enum.BlobStatusDeleting {
		t.Fatalf("row=%+v err=%v", row, err)
	}
	if err := e.db.Transaction(func(tx *gorm.DB) error {
		secured, err := blobstore.LockAvailableImageRefs(tx, original, rewritten)
		if err == nil && secured[0] != original[0] {
			t.Errorf("candidate being deleted reused: %+v", secured[0])
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if out, _ := ext.Externalize(t.Context(), original); out[0] != original[0] {
		t.Fatal("new submission reused an object being deleted")
	}
	if view, err := blobstore.NewReader(e.db, e.store).Locate(t.Context(), sha); view != nil || err != nil {
		t.Fatalf("deleting object visible: view=%+v err=%v", view, err)
	}
}

func TestPurgeReapsObjectAfterUploadStatusWriteFails(t *testing.T) {
	t.Parallel()
	e := newPurgeEnv(t)
	if err := e.db.Callback().Update().Before("gorm:update").Register("fail_blob_uploaded", func(tx *gorm.DB) {
		if values, ok := tx.Statement.Dest.(map[string]any); ok && values[constant.FieldStatus] == enum.BlobStatusUploaded {
			_ = tx.AddError(ierr.New(ierr.ErrDBUpdate, "injected uploaded status failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	raw := smallPNG(t, 24)
	sha := vo.SHA256Hex(raw)
	ext := blobstore.NewExternalizer(e.db, blobstore.NewSyncUploader(e.db, e.store))
	ext.Externalize(t.Context(), []*vo.UnifiedMessage{imageMsg(dataURI(raw))})
	row, err := dao.GetBlobDAO().GetBySHA256(e.db, sha)
	if err != nil || row == nil || row.Status != enum.BlobStatusPending || !row.ObjectMayExist || len(e.store.objects[row.StorageKey]) == 0 {
		t.Fatalf("row=%+v err=%v", row, err)
	}
	now := time.Now().UTC().Add(2 * constant.BlobPurgeGrace)
	if n, err := blobstore.PurgeUnreferenced(t.Context(), e.db, e.store, now); n != 1 || err != nil {
		t.Fatalf("purge n=%d err=%v", n, err)
	}
	if e.exists(t, row) || e.store.objects[row.StorageKey] != nil {
		t.Fatalf("row or object still present for %s", row.StorageKey)
	}
}

// TestCollectImageRefsIgnoresTextRefs 文本中出现的引用字符串不算引用
func TestCollectImageRefsIgnoresTextRefs(t *testing.T) {
	t.Parallel()
	db := openBlobDB(t)
	if err := db.AutoMigrate(&dbmodel.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ref := vo.ImageRefOf(shaOf('d'))
	m := &dbmodel.Message{CheckSum: "text", Message: &vo.UnifiedMessage{Role: enum.RoleUser, Content: &vo.UnifiedContent{
		Parts: []*vo.UnifiedContentPart{{Type: enum.ContentPartTypeText, Text: `"image_url":"` + ref + `"`}},
	}}}
	if err := db.Create(m).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	shas, err := blobstore.CollectImageRefs(t.Context(), db, []uint{m.ID})
	if err != nil || len(shas) != 0 {
		t.Fatalf("shas=%v err=%v", shas, err)
	}
}
