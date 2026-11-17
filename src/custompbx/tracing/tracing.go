// Package tracing wires OpenTelemetry distributed tracing into CustomPBX.
//
// Tracing is strictly opt-in: Setup installs an OTLP/gRPC exporter only when
// CUSTOMPBX_OTEL_ENDPOINT (or the standard OTEL_EXPORTER_OTLP_ENDPOINT) is
// set. Without it nothing is configured, the global tracer provider stays the
// OTel no-op one and every helper in this package is effectively free.
//
// One trace per call: all spans of a call share a trace ID derived
// deterministically from the call UUID (see CallTraceID), so the several ESL
// events, the webhook delivery and the CDR write of the same call land in one
// trace even though they run in different goroutines.
package tracing

import (
	"context"
	"crypto/sha256"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	// ServiceName is the OTel resource service.name.
	ServiceName = "custompbx"
	// TracerName is the instrumentation scope used by CustomPBX spans.
	TracerName = "custompbx"
	// AttrCallUUID is the span attribute carrying the call identifier.
	AttrCallUUID = "call_uuid"
)

var (
	enabled atomic.Bool
	// ratio is the sampling ratio used to decide the sampled flag of the
	// synthetic per-call parent (kept consistent with the SDK sampler).
	ratio atomic.Value // float64
)

func init() { ratio.Store(1.0) }

// Endpoint returns the configured OTLP endpoint ("" = tracing disabled).
func Endpoint() string {
	for _, k := range []string{"CUSTOMPBX_OTEL_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// SamplerRatio parses OTEL_TRACES_SAMPLER_ARG (default 1.0, clamped 0..1).
func SamplerRatio() float64 {
	v := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG"))
	if v == "" {
		return 1.0
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f != f {
		return 1.0
	}
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// grpcTarget turns an endpoint into a gRPC target plus insecure flag.
// Accepted: "tempo:4317", "http://tempo:4317" (plain), "https://host:4317" (TLS).
func grpcTarget(endpoint string) (target string, insecure bool) {
	if strings.Contains(endpoint, "://") {
		if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
			return u.Host, u.Scheme != "https"
		}
	}
	return endpoint, true
}

// Enabled reports whether Setup installed a real tracer provider.
func Enabled() bool { return enabled.Load() }

// Setup configures tracing from the environment. It returns a shutdown
// function that is always safe to call. When no endpoint is configured it is
// a no-op and Enabled() stays false.
func Setup(ctx context.Context) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	endpoint := Endpoint()
	if endpoint == "" {
		return noop, nil
	}
	target, insecure := grpcTarget(endpoint)
	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(target)}
	if insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	exp, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return noop, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(ServiceName),
	))
	if err != nil {
		return noop, err
	}
	r := SamplerRatio()
	ratio.Store(r)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(5*time.Second), sdktrace.WithMaxQueueSize(1024)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(r))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	enabled.Store(true)
	return func(c context.Context) error {
		c, cancel := context.WithTimeout(c, 5*time.Second)
		defer cancel()
		return tp.Shutdown(c)
	}, nil
}

// Tracer returns the CustomPBX tracer.
func Tracer() trace.Tracer { return otel.Tracer(TracerName) }

// CallTraceID derives a stable trace ID from a call identifier.
func CallTraceID(callID string) trace.TraceID {
	sum := sha256.Sum256([]byte("custompbx-call:" + callID))
	var id trace.TraceID
	copy(id[:], sum[:16])
	return id
}

func callParentSpanID(callID string) trace.SpanID {
	sum := sha256.Sum256([]byte("custompbx-call:" + callID))
	var id trace.SpanID
	copy(id[:], sum[16:24])
	return id
}

// CallContext returns ctx carrying the synthetic remote parent of the call's
// trace. Spans started from it join the trace of callID. When tracing is
// disabled or callID is empty ctx is returned unchanged.
func CallContext(ctx context.Context, callID string) context.Context {
	if !enabled.Load() || callID == "" {
		return ctx
	}
	tid := CallTraceID(callID)
	flags := trace.TraceFlags(0)
	if sampledByRatio(tid) {
		flags = trace.FlagsSampled
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     callParentSpanID(callID),
		TraceFlags: flags,
		Remote:     true,
	})
	return trace.ContextWithRemoteSpanContext(ctx, sc)
}

func sampledByRatio(tid trace.TraceID) bool {
	r, _ := ratio.Load().(float64)
	d := sdktrace.TraceIDRatioBased(r).ShouldSample(sdktrace.SamplingParameters{TraceID: tid}).Decision
	return d == sdktrace.RecordAndSample
}

// StartCall starts a span inside the trace of callID with attribute call_uuid.
// The caller must End the returned span. Safe (and cheap) when disabled.
func StartCall(ctx context.Context, callID, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if !enabled.Load() {
		return ctx, trace.SpanFromContext(ctx)
	}
	// If ctx already holds a span of this call, nest under it.
	if cur := trace.SpanContextFromContext(ctx); !(cur.IsValid() && cur.TraceID() == CallTraceID(callID)) {
		ctx = CallContext(ctx, callID)
	}
	all := make([]attribute.KeyValue, 0, len(attrs)+1)
	all = append(all, attribute.String(AttrCallUUID, callID))
	all = append(all, attrs...)
	return Tracer().Start(ctx, name, trace.WithAttributes(all...))
}

// RecordError marks span as failed.
func RecordError(span trace.Span, err error) {
	if err == nil || span == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
