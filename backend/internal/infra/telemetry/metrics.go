package telemetry

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// Histogram bucket boundaries
var (
	RequestDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}
	StreamDurationBuckets  = []float64{1.0, 5.0, 10.0, 30.0, 60.0, 120.0, 300.0}
)

// Global instruments — initialized with NoOp meter, re-created after init_telemetry.
var (
	ChatRequestTotal    metric.Int64Counter
	ChatRequestDuration metric.Float64Histogram
	ChatStreamDuration  metric.Float64Histogram
)

func init() {
	EnsureInstruments()
}

// EnsureInstruments creates or re-creates OTel instruments using the current MeterProvider.
func EnsureInstruments() {
	meter := otel.Meter("astron_claw")

	var err error
	ChatRequestTotal, err = meter.Int64Counter(
		"bridge.chat.requests",
		metric.WithDescription("HTTP request total (all endpoints)"),
	)
	if err != nil {
		ChatRequestTotal, _ = meter.Int64Counter("bridge.chat.requests")
	}

	ChatRequestDuration, err = meter.Float64Histogram(
		"bridge.chat.request.duration",
		metric.WithDescription("Time to first Redis result for SSE, or full duration for other endpoints"),
		metric.WithUnit("s"),
	)
	if err != nil {
		ChatRequestDuration, _ = meter.Float64Histogram("bridge.chat.request.duration")
	}

	ChatStreamDuration, err = meter.Float64Histogram(
		"bridge.chat.stream.duration",
		metric.WithDescription("SSE stream duration (/bridge/chat only)"),
		metric.WithUnit("s"),
	)
	if err != nil {
		ChatStreamDuration, _ = meter.Float64Histogram("bridge.chat.stream.duration")
	}
}

// TokenPrefix returns first 10 chars + "..." for token labels.
func TokenPrefix(token string) string {
	if len(token) > 10 {
		return token[:10] + "..."
	}
	return token
}
