package log_middleware

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/infrastructure/database"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestStatementLogKeepsSQLTemplateOnly(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(postgres.Open("host=127.0.0.1"), &gorm.Config{
		DryRun: true, DisableAutomaticPing: true,
		Logger: &database.GormLoggerAdapter{LogLevel: gormlogger.Info},
	})
	if err != nil {
		t.Fatal(err)
	}
	traceID := uuid.NewString()
	ctx := context.WithValue(t.Context(), constant.CtxKeyTraceID, traceID)
	if err := db.WithContext(ctx).Exec("SELECT ?::text", "private-database-parameter").Error; err != nil {
		t.Fatal(err)
	}
	entry := traceLog(t, traceID, "db.statement.finished")
	if !strings.Contains(entry, "SELECT $1") || strings.Contains(entry, "private-database-parameter") {
		t.Fatalf("unexpected statement log %s", entry)
	}
}
