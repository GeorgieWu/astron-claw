package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/log"
	otellog "go.opentelemetry.io/otel/log/global"
)

var ChatLogger log.Logger

func EnsureLogger() {
	ChatLogger = otellog.GetLoggerProvider().Logger("astron-claw/chat")
}

type ChatLogRecord struct {
	TokenID     string
	SessionID   string
	DurationMs  float64
	TTFBMs      float64
	Code        int
	CloseReason string
	PodIP       string
	TraceID     string
}

func EmitChatLog(ctx context.Context, rec ChatLogRecord) {
	severity, severityText := severityFromCode(rec.Code)

	var r log.Record
	r.SetTimestamp(time.Now())
	r.SetSeverity(severity)
	r.SetSeverityText(severityText)
	r.SetBody(log.StringValue("chat.request.completed"))
	r.AddAttributes(
		log.String("token_id", rec.TokenID),
		log.String("session_id", rec.SessionID),
		log.Float64("duration_ms", rec.DurationMs),
		log.Float64("ttfb_ms", rec.TTFBMs),
		log.Int("code", rec.Code),
		log.String("close_reason", rec.CloseReason),
		log.String("pod_ip", rec.PodIP),
		log.String("trace_id", rec.TraceID),
	)

	ChatLogger.Emit(ctx, r)
}

func severityFromCode(code int) (log.Severity, string) {
	if code == 0 {
		return log.SeverityInfo, "INFO"
	}
	if isClientError(code) {
		return log.SeverityWarn, "WARN"
	}
	return log.SeverityError, "ERROR"
}

var clientErrorCodes = map[int]bool{
	10200: true, // CodeChatEmptyMessage
	10201: true, // CodeChatNoBot
	10202: true, // CodeChatInvalidReq
	10204: true, // CodeChatStreamTimeout
	10206: true, // CodeChatStreamUnsupported
	10300: true, // CodeMediaFileTooLarge
	10301: true, // CodeMediaInvalidFile
	10302: true, // CodeMediaBadURLScheme
	10303: true, // CodeMediaUnsupportedType
	10304: true, // CodeMediaTooMany
	10400: true, // CodeSessionNotFound
}

func isClientError(code int) bool {
	return clientErrorCodes[code]
}
