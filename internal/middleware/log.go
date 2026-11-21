package middleware

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"github.com/hcd233/aris-mass/internal/common/constant"
	"github.com/hcd233/aris-mass/internal/logger"
	"github.com/samber/lo"
	"go.uber.org/zap"
)

// LogSamplingRule limits successful access logs for one path.
type LogSamplingRule struct {
	Path     string
	Interval time.Duration
}

type LogMiddlewareConfig struct {
	SamplingRules []LogSamplingRule
}

type logSampler struct {
	mu       sync.Mutex
	lastLogs map[string]time.Time
}

func (s *logSampler) shouldLog(path string, interval time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if last, ok := s.lastLogs[path]; ok && now.Sub(last) < interval {
		return false
	}
	s.lastLogs[strings.Clone(path)] = now
	return true
}

type loggedBusinessError struct {
	Code int `json:"code"`
}

type loggedBusinessEnvelope struct {
	Error loggedBusinessError `json:"error"`
}

func queryLogFields(c fiber.Ctx) []zap.Field {
	args := c.Request().URI().QueryArgs()
	var fields []zap.Field
	for _, key := range constant.LogQueryNumberParams {
		raw := args.Peek(key)
		if len(raw) == 0 || len(raw) > constant.LogIdentifierMaxBytes {
			continue
		}
		if _, err := strconv.ParseUint(string(raw), constant.DecimalBase, constant.ParseIntBitSize); err == nil {
			fields = append(fields, zap.String(key, string(raw)))
		}
	}
	for _, key := range constant.LogQueryStringParams {
		if value := logger.Identifier(string(args.Peek(key))); value != "" {
			fields = append(fields, zap.String(key, value))
		}
	}
	return fields
}

func responseBusinessCode(c fiber.Ctx) int {
	resp := c.Response()
	if resp.IsBodyStream() || len(resp.Header.Peek(fiber.HeaderContentEncoding)) != 0 ||
		!logger.IsJSONContentType(string(resp.Header.ContentType())) {
		return 0
	}
	body := resp.Body()
	if len(body) == 0 || len(body) > constant.LogErrorInspectMaxBytes {
		return 0
	}
	var envelope loggedBusinessEnvelope
	if sonic.Unmarshal(body, &envelope) != nil {
		return 0
	}
	return envelope.Error.Code
}

// LogMiddleware records response preparation. Stream execution is logged by the stream callback.
func LogMiddleware(cfg LogMiddlewareConfig) fiber.Handler {
	samplingIndex := lo.SliceToMap(cfg.SamplingRules, func(rule LogSamplingRule) (string, time.Duration) {
		return rule.Path, rule.Interval
	})
	sampler := &logSampler{lastLogs: make(map[string]time.Time, len(cfg.SamplingRules))}

	return func(c fiber.Ctx) error {
		start := time.Now()
		path := strings.Clone(c.Path())
		err := c.Next()
		status := c.Response().StatusCode()
		if err != nil {
			var fiberErr *fiber.Error
			if errors.As(err, &fiberErr) {
				status = fiberErr.Code
			} else {
				status = fiber.StatusInternalServerError
			}
		}
		bizCode := responseBusinessCode(c)
		if err == nil && status < fiber.StatusBadRequest && bizCode == 0 {
			if interval, ok := samplingIndex[path]; ok && !sampler.shouldLog(path, interval) {
				return nil
			}
		}

		outcome := constant.LogOutcomeSuccess
		level := zap.InfoLevel
		switch {
		case status >= fiber.StatusInternalServerError:
			outcome, level = constant.LogOutcomeServerError, zap.ErrorLevel
		case status >= fiber.StatusBadRequest:
			outcome, level = constant.LogOutcomeClientError, zap.WarnLevel
		case bizCode != 0:
			outcome, level = constant.LogOutcomeBusinessError, zap.WarnLevel
		}

		fields := []zap.Field{
			zap.String("event", "http.request.finished"),
			zap.Int("status", status),
			zap.String("method", c.Method()),
			zap.String("outcome", outcome),
			zap.Int64("durationMs", time.Since(start).Milliseconds()),
			zap.Bool("stream", c.Response().IsBodyStream()),
		}
		if c.Matched() {
			fields = append(fields, zap.String("path", c.Route().Path))
		}
		if bizCode != 0 {
			fields = append(fields, zap.Int("bizErrorCode", bizCode))
		}
		if query := queryLogFields(c); len(query) > 0 {
			fields = append(fields, zap.Dict("queryParams", query...))
		}
		if !c.Request().IsBodyStream() {
			inspectJSON := len(c.Request().Header.Peek(fiber.HeaderContentEncoding)) == 0 &&
				logger.IsJSONContentType(c.Get(fiber.HeaderContentType))
			fields = append(fields, logger.RequestFields(c.BodyRaw(), inspectJSON)...)
		}
		fields = append(fields, logger.ErrorFields(err)...)

		logger.WithFCtx(c).Log(level, "[LogMiddleware] Request finished", fields...)
		return err
	}
}
