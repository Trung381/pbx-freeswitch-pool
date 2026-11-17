package tracing

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestSetupDisabledWithoutEndpoint(t *testing.T) {
	for _, k := range []string{"CUSTOMPBX_OTEL_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"} {
		t.Setenv(k, "")
	}
	shutdown, err := Setup(context.Background())
	if err != nil || shutdown == nil {
		t.Fatalf("Setup err=%v shutdown nil=%v", err, shutdown == nil)
	}
	if Enabled() {
		t.Fatal("tracing must stay disabled without endpoint")
	}
	ctx, span := StartCall(context.Background(), "call-1", "x")
	span.End()
	if trace.SpanContextFromContext(ctx).IsValid() {
		t.Fatal("disabled tracing must not produce a valid span context")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEndpointPrecedenceAndTarget(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "std:4317")
	if Endpoint() != "std:4317" {
		t.Fatalf("got %q", Endpoint())
	}
	t.Setenv("CUSTOMPBX_OTEL_ENDPOINT", "tempo:4317")
	if Endpoint() != "tempo:4317" {
		t.Fatalf("got %q", Endpoint())
	}
	cases := map[string]struct {
		target   string
		insecure bool
	}{
		"tempo:4317":         {"tempo:4317", true},
		"http://tempo:4317":  {"tempo:4317", true},
		"https://tempo:4317": {"tempo:4317", false},
	}
	for in, want := range cases {
		g, ins := grpcTarget(in)
		if g != want.target || ins != want.insecure {
			t.Errorf("%s -> %s,%v", in, g, ins)
		}
	}
}

func TestSamplerRatio(t *testing.T) {
	cases := map[string]float64{"": 1, "0.25": 0.25, "abc": 1, "-1": 0, "7": 1}
	for in, want := range cases {
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", in)
		if got := SamplerRatio(); got != want {
			t.Errorf("%q -> %v want %v", in, got, want)
		}
	}
}

func TestCallTraceIDDeterministic(t *testing.T) {
	if CallTraceID("a") != CallTraceID("a") || CallTraceID("a") == CallTraceID("b") {
		t.Fatal("trace id must be stable per call and differ across calls")
	}
	if !CallTraceID("a").IsValid() {
		t.Fatal("invalid trace id")
	}
}

func TestStartCallSharesTraceAcrossSpans(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(1))))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	enabled.Store(true)
	ratio.Store(1.0)
	t.Cleanup(func() { enabled.Store(false); otel.SetTracerProvider(prev) })

	ctx1, s1 := StartCall(context.Background(), "call-42", "esl.channel_create")
	_, child := StartCall(ctx1, "call-42", "callwebhook.deliver")
	child.End()
	s1.End()
	_, s2 := StartCall(context.Background(), "call-42", "esl.channel_hangup")
	s2.End()
	_, other := StartCall(context.Background(), "call-43", "esl.channel_create")
	other.End()

	spans := exp.GetSpans()
	if len(spans) != 4 {
		t.Fatalf("spans=%d", len(spans))
	}
	want := CallTraceID("call-42")
	for _, s := range spans[:3] {
		if s.SpanContext.TraceID() != want {
			t.Errorf("%s trace %s want %s", s.Name, s.SpanContext.TraceID(), want)
		}
		found := false
		for _, a := range s.Attributes {
			if string(a.Key) == AttrCallUUID && a.Value.AsString() == "call-42" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s missing call_uuid", s.Name)
		}
	}
	if spans[0].Parent.SpanID() != spans[1].SpanContext.SpanID() {
		t.Error("webhook span should be child of channel span")
	}
	if spans[3].SpanContext.TraceID() == want {
		t.Error("different call must use a different trace")
	}
}

func TestZeroRatioNotSampled(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0))))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	enabled.Store(true)
	ratio.Store(0.0)
	t.Cleanup(func() { enabled.Store(false); ratio.Store(1.0); otel.SetTracerProvider(prev) })
	_, s := StartCall(context.Background(), "c", "x")
	s.End()
	if n := len(exp.GetSpans()); n != 0 {
		t.Fatalf("spans=%d", n)
	}
}
