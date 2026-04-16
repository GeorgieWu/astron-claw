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
	LogType   string
	AppID     string
	SessionID string
	FALR      float64
	FAFR      float64
	Ret       int
	IP        string
	TraceID   string
	Func      string
}

func EmitChatLog(ctx context.Context, rec ChatLogRecord) {
	logType := rec.LogType
	if logType == "" {
		logType = "server_log"
	}

	var r log.Record
	r.SetTimestamp(time.Now())
	r.AddAttributes(
		log.String("log_type", logType),
		log.String("appid", rec.AppID),
		log.String("session_id", rec.SessionID),
		log.Float64("falr", rec.FALR),
		log.Float64("fafr", rec.FAFR),
		log.Int("ret", rec.Ret),
		log.String("ip", rec.IP),
		log.String("trace_id", rec.TraceID),
		log.String("func", rec.Func),
	)

	ChatLogger.Emit(ctx, r)
}
