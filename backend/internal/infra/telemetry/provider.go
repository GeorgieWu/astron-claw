package telemetry

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"astron-claw/backend/internal/config"
)

var provider *sdkmetric.MeterProvider

// Init initializes OTel MeterProvider with OTLP gRPC exporter.
func Init(ctx context.Context, otlpCfg config.OtlpConfig) error {
	if !otlpCfg.Enabled {
		log.Info().Msg("OTLP telemetry disabled (OTLP_ENABLED=false)")
		return nil
	}

	if !otlpCfg.MetricsEnabled {
		log.Info().Msg("OTLP metrics disabled")
		return nil
	}

	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(otlpCfg.Endpoint),
	}
	if otlpCfg.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}

	exporter, err := otlpmetricgrpc.New(ctx, opts...)
	if err != nil {
		return err
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(otlpCfg.ServiceName),
	)

	// Custom bucket boundaries
	requestDurationView := sdkmetric.NewView(
		sdkmetric.Instrument{Name: "bridge.chat.request.duration"},
		sdkmetric.Stream{
			Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: RequestDurationBuckets,
			},
		},
	)
	streamDurationView := sdkmetric.NewView(
		sdkmetric.Instrument{Name: "bridge.chat.stream.duration"},
		sdkmetric.Stream{
			Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: StreamDurationBuckets,
			},
		},
	)

	provider = sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(exporter,
				sdkmetric.WithInterval(10*time.Second),
			),
		),
		sdkmetric.WithView(requestDurationView, streamDurationView),
	)
	otel.SetMeterProvider(provider)

	log.Info().
		Str("service", otlpCfg.ServiceName).
		Str("endpoint", otlpCfg.Endpoint).
		Msg("OTLP metrics exporter initialised (gRPC)")

	return nil
}

// Shutdown gracefully shuts down all providers.
func Shutdown() {
	if provider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := provider.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("OTLP telemetry shutdown error")
		}
		log.Info().Msg("OTLP telemetry shut down")
		provider = nil
	}
}
