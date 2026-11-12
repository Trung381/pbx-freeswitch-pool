// Package promexp exposes Prometheus metrics for CustomPBX.
//
// It intentionally has no dependency on other CustomPBX packages so that fsesl,
// hepHandler and main can all import it without creating cycles. Values that
// live in other packages are provided through callbacks registered at start-up.
package promexp

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	registry = prometheus.NewRegistry()

	callsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "pbx_calls_total",
		Help: "Completed calls (primary leg) by hangup cause and direction.",
	}, []string{"hangup_cause", "direction"})

	hepPackets = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "pbx_hep_packets_total",
		Help: "HEP packets by result (received, decode_error).",
	}, []string{"result"})

	eslReconnects = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pbx_esl_reconnect_attempts_total",
		Help: "Number of ESL reconnect attempts made by the connect keeper.",
	})

	gatewayMu    sync.Mutex
	gatewayState = map[string]string{}
)

// Callbacks supplied by main; nil callbacks yield 0.
var (
	cbMu          sync.RWMutex
	eslConnected  func() bool
	dbConnected   func() bool
	channelCounts func() (total, answered int)
)

// SetCallbacks wires gauges to live state owned by other packages.
func SetCallbacks(esl, db func() bool, channels func() (int, int)) {
	cbMu.Lock()
	defer cbMu.Unlock()
	eslConnected, dbConnected, channelCounts = esl, db, channels
}

func boolGauge(f func() bool) float64 {
	if f != nil && f() {
		return 1
	}
	return 0
}

// ObserveCallEnd counts a finished primary call leg.
func ObserveCallEnd(hangupCause, direction string) {
	if hangupCause == "" {
		hangupCause = "UNKNOWN"
	}
	if direction == "" {
		direction = "unknown"
	}
	callsTotal.WithLabelValues(hangupCause, direction).Inc()
}

// ObserveHEP counts HEP packets; result is "received" or "decode_error".
func ObserveHEP(result string) { hepPackets.WithLabelValues(result).Inc() }

// ObserveESLReconnect counts reconnect attempts.
func ObserveESLReconnect() { eslReconnects.Inc() }

// SetGatewayState records the latest sofia::gateway_state value of a gateway.
// An empty state removes the gateway (deleted).
func SetGatewayState(name, state string) {
	if name == "" {
		return
	}
	gatewayMu.Lock()
	defer gatewayMu.Unlock()
	if state == "" {
		delete(gatewayState, name)
		return
	}
	gatewayState[name] = state
}

type gatewayCollector struct {
	reged *prometheus.Desc
	state *prometheus.Desc
}

func (c *gatewayCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.reged
	ch <- c.state
}

func (c *gatewayCollector) Collect(ch chan<- prometheus.Metric) {
	gatewayMu.Lock()
	defer gatewayMu.Unlock()
	for name, st := range gatewayState {
		v := 0.0
		if strings.EqualFold(st, "REGED") {
			v = 1
		}
		ch <- prometheus.MustNewConstMetric(c.reged, prometheus.GaugeValue, v, name)
		ch <- prometheus.MustNewConstMetric(c.state, prometheus.GaugeValue, 1, name, st)
	}
}

func init() {
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		callsTotal, hepPackets, eslReconnects,
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "pbx_esl_connected",
			Help: "1 when CustomPBX has a live ESL connection to FreeSWITCH.",
		}, func() float64 { cbMu.RLock(); defer cbMu.RUnlock(); return boolGauge(eslConnected) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "pbx_database_connected",
			Help: "1 when CustomPBX has a live database connection.",
		}, func() float64 { cbMu.RLock(); defer cbMu.RUnlock(); return boolGauge(dbConnected) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "pbx_channels",
			Help: "Channels currently tracked in the channel cache.",
		}, func() float64 {
			cbMu.RLock()
			defer cbMu.RUnlock()
			if channelCounts == nil {
				return 0
			}
			t, _ := channelCounts()
			return float64(t)
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "pbx_calls_active",
			Help: "Answered channels currently active.",
		}, func() float64 {
			cbMu.RLock()
			defer cbMu.RUnlock()
			if channelCounts == nil {
				return 0
			}
			_, a := channelCounts()
			return float64(a)
		}),
		&gatewayCollector{
			reged: prometheus.NewDesc("pbx_gateway_registered", "1 when the sofia gateway state is REGED.", []string{"gateway"}, nil),
			state: prometheus.NewDesc("pbx_gateway_state", "Latest sofia gateway state (value is always 1).", []string{"gateway", "state"}, nil),
		},
	)
}

// Handler returns the /metrics HTTP handler.
func Handler() http.Handler { return promhttp.HandlerFor(registry, promhttp.HandlerOpts{}) }

// Serve starts a dedicated metrics listener. It is separate from the public web
// router on purpose: the address is not published by docker-compose, so it is
// reachable only from the Docker network (Prometheus), never through nginx or
// the published 8080-8082 ports. An empty addr disables it.
func Serve(addr string) {
	if addr == "" {
		logger.Info("Prometheus metrics disabled")
		return
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", Handler())
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
	}
	logger.Info("Prometheus metrics listening", "addr", addr, "path", "/metrics")
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("Prometheus metrics listener stopped", "err", err)
	}
}
