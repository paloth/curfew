package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// InitTracer initializes the OpenTelemetry tracer with Jaeger backend
// Returns a shutdown function that should be called on application exit
func InitTracer(serviceName, serviceVersion, jaegerEndpoint string) (func(context.Context) error, error) {
	// Schemaless attributes: merging with resource.Default() fails whenever the SDK's semconv version differs from ours.
	res, err := resource.New(
		context.Background(),
		resource.WithTelemetrySDK(),
		resource.WithFromEnv(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// OTLP HTTP exporter (Jaeger listens on 4318). TLS by default;
	// OTEL_EXPORTER_OTLP_INSECURE=true switches to plain HTTP, e.g. for a local Jaeger.
	exporter, err := otlptracehttp.New(
		context.Background(),
		otlptracehttp.WithEndpoint(jaegerEndpoint),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Tracers from otel.Tracer pick this up even if they were created before InitTracer.
	otel.SetTracerProvider(tp)

	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		if err := tp.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown tracer provider: %w", err)
		}
		return nil
	}, nil
}
