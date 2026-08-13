package observability

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type OTelMetrics struct {
	Meter                   metric.Meter
	ExtractionCounter       metric.Int64Counter
	SyncDurationHistogram   metric.Float64Histogram
	SyncCounter             metric.Int64Counter
	CalendarValidateCounter metric.Int64Counter
	Shutdown                func(context.Context) error
}

func InitOTel(serviceName string) *OTelMetrics {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		slog.Info("OTEL_EXPORTER_OTLP_ENDPOINT not set; OpenTelemetry push exporter disabled")
		return nil
	}

	headersStr := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")
	headers := make(map[string]string)
	if headersStr != "" {
		pairs := strings.Split(headersStr, ",")
		for _, pair := range pairs {
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) == 2 {
				headers[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
	}

	trimmedEndpoint := strings.TrimPrefix(endpoint, "https://")
	trimmedEndpoint = strings.TrimPrefix(trimmedEndpoint, "http://")
	if idx := strings.Index(trimmedEndpoint, "/"); idx != -1 {
		trimmedEndpoint = trimmedEndpoint[:idx]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(trimmedEndpoint),
		otlpmetrichttp.WithURLPath("/v1/metrics"),
	}
	if strings.HasPrefix(endpoint, "http://") {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	if len(headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(headers))
	}

	exporter, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		slog.Error("failed to create OTLP metric exporter", "error", err)
		return nil
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		slog.Error("failed to create OTel resource", "error", err)
		res = resource.Default()
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(15*time.Second))),
	)
	otel.SetMeterProvider(provider)

	meter := provider.Meter("contestsync")

	extractionCounter, _ := meter.Int64Counter(
		"contestsync.contests.extracted",
		metric.WithDescription("Total contests extracted by platform"),
	)
	syncDuration, _ := meter.Float64Histogram(
		"contestsync.user.sync.duration",
		metric.WithDescription("Duration of user calendar synchronization in seconds"),
	)
	syncCounter, _ := meter.Int64Counter(
		"contestsync.users.synced",
		metric.WithDescription("Total user calendar sync attempts"),
	)
	validateCounter, _ := meter.Int64Counter(
		"contestsync.calendar.validations",
		metric.WithDescription("Total calendar access validation checks"),
	)

	slog.Info("OpenTelemetry OTLP push metric exporter initialized successfully", "endpoint", trimmedEndpoint, "service", serviceName)

	return &OTelMetrics{
		Meter:                   meter,
		ExtractionCounter:       extractionCounter,
		SyncDurationHistogram:   syncDuration,
		SyncCounter:             syncCounter,
		CalendarValidateCounter: validateCounter,
		Shutdown:                provider.Shutdown,
	}
}
