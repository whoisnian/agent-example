package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// Tracer wires up an OpenTelemetry tracer provider and W3C propagator.
//
// If `otlpEndpoint` is empty, a no-op exporter is installed: spans are still
// created (so trace_id propagates through logs) but nothing is exported.
// This is the documented default for dev / unit-test environments.
type Tracer struct {
	provider *sdktrace.TracerProvider
}

// SetupTracing installs the global tracer provider + propagator and returns a
// handle for graceful shutdown.
func SetupTracing(ctx context.Context, serviceName, otlpEndpoint string) (*Tracer, error) {
	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	}

	if otlpEndpoint != "" {
		exporter, err := otlptrace.New(ctx,
			otlptracegrpc.NewClient(
				otlptracegrpc.WithEndpoint(otlpEndpoint),
				otlptracegrpc.WithInsecure(),
			),
		)
		if err != nil {
			return nil, fmt.Errorf("otlp exporter: %w", err)
		}
		opts = append(opts, sdktrace.WithBatcher(exporter))
	}

	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return &Tracer{provider: tp}, nil
}

// Shutdown flushes pending spans, bounded by the given timeout.
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t == nil || t.provider == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return t.provider.Shutdown(shutdownCtx)
}

// TracerProvider returns the configured TracerProvider. Useful for tests.
func (t *Tracer) TracerProvider() trace.TracerProvider {
	return t.provider
}
