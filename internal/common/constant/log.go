package constant

import "time"

const (
	LogMiddlewareSamplingInterval = 5 * time.Minute

	LogRequestInspectMaxBytes = 1 << 20
	LogErrorInspectMaxBytes   = 8 << 10
	LogIdentifierMaxBytes     = 128
	LogSQLTemplateMaxBytes    = 2048

	LogIdentifierPunctuation = "._:/-[]"
	HTTPMediaTypeJSONSuffix  = "+json"

	LogOutcomeSuccess       = "success"
	LogOutcomeFailed        = "failed"
	LogOutcomeIncomplete    = "incomplete"
	LogOutcomeServerError   = "server_error"
	LogOutcomeClientError   = "client_error"
	LogOutcomeBusinessError = "business_error"

	LogErrorKindInternal        = "internal"
	LogErrorKindCanceled        = "canceled"
	LogErrorKindTimeout         = "timeout"
	LogErrorKindUnexpectedEOF   = "unexpected_eof"
	LogErrorKindNetwork         = "network"
	LogErrorKindUpstreamHTTP    = "upstream_http"
	LogErrorKindCircuitOpen     = "circuit_open"
	LogErrorKindBulkheadFull    = "bulkhead_full"
	LogErrorKindOAuthCredential = "oauth_credential"

	LogInfoMaxSizeMB  = 100
	LogInfoMaxBackups = 3
	LogInfoMaxAgeDays = 7
	LogErrMaxSizeMB   = 500
	LogErrMaxBackups  = 3
	LogErrMaxAgeDays  = 30

	LogInfoFileName  = "aris-mass.log"
	LogErrFileName   = "aris-mass-error.log"
	LogPanicFileName = "aris-mass-panic.log"

	CLSFieldMessage   = "message"
	CLSFieldLevel     = "level"
	CLSFieldTimestamp = "timestamp"
	CLSFieldCaller    = "caller"
	CLSFieldStack     = "stack"

	CLSProducerCloseTimeoutMs = 10000
)

var (
	LogQueryNumberParams        = [...]string{"id", "endpointID", "sessionID", "apiKeyID", "page", "pageSize", "limit", "offset"}
	LogQueryStringParams        = [...]string{"model", "upstreamModel", "protocol", "sort", "sortField", "stream", "status", "capability", "missingOnly"}
	LogUpstreamRequestIDHeaders = [...]string{"Request-Id", "X-Request-Id", "X-Amzn-RequestId"}
)
