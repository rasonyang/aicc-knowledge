// SPDX-License-Identifier: Apache-2.0

// Package obs wires logging, tracing and metrics: slog (text in dev, JSON in
// prod) with trace ids, OTLP/HTTP tracing only when an endpoint is configured,
// and the OTel Prometheus exporter behind /metrics.
package obs

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Options configure Setup.
type Options struct {
	ServiceName  string
	Version      string
	LogLevel     string
	OTLPEndpoint string // empty turns tracing off
	Dev          bool
	LogWriter    *os.File // defaults to stderr
}

// Providers owns the telemetry the process set up.
type Providers struct {
	// Metrics holds the instruments; MetricsHandler serves /metrics.
	Metrics        *Metrics
	MetricsHandler http.Handler
	shutdown       []func(context.Context) error
}

// Setup installs the default slog logger and the global OTel providers.
func Setup(ctx context.Context, o Options) (*Providers, error) {
	w := o.LogWriter
	if w == nil {
		w = os.Stderr
	}
	slog.SetDefault(NewLogger(w, o.LogLevel, o.Dev))

	res, err := resource.Merge(resource.Default(),
		resource.NewSchemaless(semconv.ServiceName(o.ServiceName), semconv.ServiceVersion(o.Version)))
	if err != nil {
		return nil, err
	}
	p := &Providers{}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	exporter, err := otelprom.New(otelprom.WithRegisterer(reg), otelprom.WithoutScopeInfo())
	if err != nil {
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter), sdkmetric.WithResource(res))
	otel.SetMeterProvider(mp)
	p.shutdown = append(p.shutdown, mp.Shutdown)
	p.MetricsHandler = promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	if p.Metrics, err = NewMetrics(mp.Meter(o.ServiceName)); err != nil {
		return nil, err
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if o.OTLPEndpoint != "" {
		exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(o.OTLPEndpoint))
		if err != nil {
			return nil, err
		}
		tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
		otel.SetTracerProvider(tp)
		p.shutdown = append(p.shutdown, tp.Shutdown)
	}
	return p, nil
}

// Shutdown flushes and stops the providers.
func (p *Providers) Shutdown(ctx context.Context) error {
	var errs []error
	for i := len(p.shutdown) - 1; i >= 0; i-- {
		errs = append(errs, p.shutdown[i](ctx))
	}
	return errors.Join(errs...)
}

// NewLogger builds the process logger: text in dev, JSON in prod, with the
// trace and span ids of the context added to every record.
func NewLogger(w *os.File, level string, dev bool) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if dev {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(traceHandler{h})
}

type traceHandler struct{ slog.Handler }

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("traceId", sc.TraceID().String()), slog.String("spanId", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(a)}
}
func (h traceHandler) WithGroup(n string) slog.Handler { return traceHandler{h.Handler.WithGroup(n)} }
