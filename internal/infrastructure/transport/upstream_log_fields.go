package transport

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/common/model"
	"github.com/hcd233/aris-mass/internal/domain/llmproxy/vo"
	"github.com/hcd233/aris-mass/internal/infrastructure/httpclient"
	"github.com/hcd233/aris-mass/internal/logger"
	"github.com/hcd233/aris-mass/internal/util"
	"go.uber.org/zap"
)

func upstreamFields(ctx context.Context, ep vo.UpstreamEndpoint, operation string) []zap.Field {
	return []zap.Field{
		zap.Uint("endpointAttempt", util.CtxValueUint(ctx, constant.CtxKeyEndpointAttempt)),
		zap.String("upstreamOperation", operation),
		zap.String("upstreamModel", logger.Identifier(ep.Model)),
		zap.String("upstreamAuthType", string(ep.AuthType)),
	}
}

type providerError struct {
	Type  string                 `json:"type"`
	Code  sonic.NoCopyRawMessage `json:"code"`
	Param string                 `json:"param"`
}

type providerErrorEnvelope struct {
	Error providerError `json:"error"`
}

func providerErrorFields(body []byte) []zap.Field {
	fields := []zap.Field{zap.Int("upstreamErrorBytes", len(body))}
	if len(body) == 0 || len(body) > constant.LogErrorInspectMaxBytes {
		return fields
	}
	var envelope providerErrorEnvelope
	if sonic.Unmarshal(body, &envelope) != nil {
		return fields
	}
	var code string
	if len(envelope.Error.Code) != 0 && sonic.Unmarshal(envelope.Error.Code, &code) != nil {
		var number int64
		if sonic.Unmarshal(envelope.Error.Code, &number) == nil {
			code = strconv.FormatInt(number, constant.DecimalBase)
		}
	}
	return append(fields,
		zap.String("upstreamErrorType", logger.Identifier(envelope.Error.Type)),
		zap.String("upstreamErrorCode", logger.Identifier(code)),
		zap.String("upstreamErrorParam", logger.Identifier(envelope.Error.Param)),
	)
}

// sendUpstreamHTTP observes exactly one HTTP attempt and preserves the provider response for callers.
func sendUpstreamHTTP(ctx context.Context, module, operation string, ep vo.UpstreamEndpoint, attempt int, req *http.Request, body []byte) (*http.Response, error) {
	log := logger.WithCtx(ctx).With(upstreamFields(ctx, ep, operation)...).With(
		zap.Int("upstreamAttempt", attempt),
		zap.String("upstreamHost", logger.Identifier(req.URL.Hostname())),
	)
	log.Info("["+module+"] Sending upstream request", append(logger.RequestFields(body, true),
		zap.String("event", "llm.upstream.request"),
		zap.String("anthropicVersion", logger.Identifier(req.Header.Get(constant.HTTPHeaderAnthropicVersion))),
	)...)

	start := time.Now()
	resp, err := httpclient.GetEndpointHTTPClient(ep.AuthType).Do(req)
	fields := []zap.Field{
		zap.String("event", "llm.upstream.result"),
		zap.Int64("durationMs", time.Since(start).Milliseconds()),
	}
	if err != nil {
		log.Warn("["+module+"] Upstream connection failed", append(fields, logger.ErrorFields(err)...)...)
		return nil, &model.UpstreamConnectionError{Cause: err}
	}

	fields = append(fields, zap.Int("upstreamStatusCode", resp.StatusCode))
	for _, header := range constant.LogUpstreamRequestIDHeaders {
		if id := logger.Identifier(resp.Header.Get(header)); id != "" {
			fields = append(fields, zap.String("upstreamRequestID", id))
			break
		}
	}
	if resp.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(resp.Body) //nolint:errcheck // preserve the existing best-effort error response
		_ = resp.Body.Close()                 //nolint:errcheck // close best effort on error path
		log.Warn("["+module+"] Upstream rejected request", append(fields, providerErrorFields(errorBody)...)...)
		return nil, &model.UpstreamError{
			StatusCode: resp.StatusCode,
			Headers:    capturePassthroughResponseHeaders(resp.Header),
			Body:       string(errorBody),
		}
	}

	log.Info("["+module+"] Upstream response headers received", fields...)
	storePassthroughResponseHeaders(ctx, resp.Header)
	return resp, nil
}
