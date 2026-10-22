package observability

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/koobam/wuzz-backend/internal/pkg/config"
)

// Setup wires distributed tracing to the OTLP collector.
//
// Traces are pushed, metrics are pulled: Prometheus scrapes /metrics (see
// metrics.go), so a collector restart loses no counters, and only spans travel
// over OTLP. With no OTEL_EXPORTER_OTLP_ENDPOINT, tracing stays off but trace
// context still propagates, so a request id minted upstream is not lost.
//
// The returned function flushes the batch processor; call it on shutdown or
// the spans of the request that triggered the deploy are dropped.
func Setup(ctx context.Context, cfg *config.Config) (func(context.Context) error, error) {
	// W3C traceparent on every transport: HTTP at the gateway, gRPC between
	// services, Kafka headers into consumers.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if cfg.Telemetry.OTLPEndpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.Version),
			attribute.String("deployment.environment", cfg.Env),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: build resource: %w", err)
	}

	// The endpoint is a bare host:port inside the compose network; TLS is not
	// worth it between containers on one host.
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.Telemetry.OTLPEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler(cfg.Telemetry.TraceRatio, cfg.ServiceName == "wuzz-"+string(config.ServiceGateway))),
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)),
	)
	otel.SetTracerProvider(tp)

	return func(ctx context.Context) error { return errors.Join(tp.Shutdown(ctx)) }, nil
}

// sampler keeps a trace whole: ParentBased makes every downstream service
// follow the gateway's decision instead of rolling its own dice and producing
// half a trace. The gateway is the exception — its parent is a traceparent
// header written by a phone or browser, and trusting that flag would let a
// client set it on every request and make production trace everything. So the
// edge applies its own ratio and still adopts the inbound trace id.
func sampler(ratio float64, edge bool) sdktrace.Sampler {
	root := sdktrace.TraceIDRatioBased(ratio)
	if !edge {
		return sdktrace.ParentBased(root)
	}
	return sdktrace.ParentBased(root,
		sdktrace.WithRemoteParentSampled(root),
		sdktrace.WithRemoteParentNotSampled(root),
	)
}
