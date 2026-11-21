package log_middleware

import (
	"strings"
	"testing"

	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/logger"
)

func TestRequestSummaryOmitsUninspectableBodiesInCLS(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, body, status string
		isJSON             bool
	}{
		{"too large", strings.Repeat("private-content", constant.LogRequestInspectMaxBytes/8), "size_limit", true},
		{"invalid JSON", `{"messages":["private-content"`, "invalid_json", true},
		{"form", "password=private-content", "unsupported_content", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fields := logger.EncodeCLSFields(logger.RequestFields([]byte(tc.body), tc.isJSON))
			if fields["requestSummaryStatus"] != tc.status || fields["requestBytes"] == "" {
				t.Fatalf("unexpected summary fields: %v", fields)
			}
			for _, value := range fields {
				if strings.Contains(value, "private-content") {
					t.Fatal("raw payload reached CLS fields")
				}
			}
		})
	}
}
