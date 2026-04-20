package telemetry

import (
	"context"
	"encoding/json"
	"time"

	"go.opentelemetry.io/otel/log"
	otellog "go.opentelemetry.io/otel/log/global"
)

var ChatLogger log.Logger

func EnsureLogger() {
	ChatLogger = otellog.GetLoggerProvider().Logger("astron-claw/chat")
}

type ChatLogRecord struct {
	LogType   string  `json:"log_type"`
	AppID     string  `json:"appid"`
	SessionID string  `json:"session_id"`
	FALR      float64 `json:"falr"`
	FAFR      float64 `json:"fafr"`
	Ret       int     `json:"ret"`
	IP        string  `json:"ip"`
	TraceID   string  `json:"trace_id"`
	Func      string  `json:"func"`
}

func EmitChatLog(ctx context.Context, rec ChatLogRecord) {
	if rec.LogType == "" {
		rec.LogType = "server_log"
	}

	body, _ := json.Marshal(rec)

	var r log.Record
	r.SetTimestamp(time.Now())
	r.SetBody(log.StringValue(string(body)))

	ChatLogger.Emit(ctx, r)
}
