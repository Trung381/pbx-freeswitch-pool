package promexp

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsExposed(t *testing.T) {
	SetCallbacks(func() bool { return true }, func() bool { return false }, func() (int, int) { return 3, 2 })
	ObserveCallEnd("NORMAL_CLEARING", "inbound")
	ObserveHEP("received")
	SetGatewayState("trunk1", "REGED")
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		"pbx_esl_connected 1", "pbx_database_connected 0", "pbx_calls_active 2", "pbx_channels 3",
		`pbx_calls_total{direction="inbound",hangup_cause="NORMAL_CLEARING"} 1`,
		`pbx_hep_packets_total{result="received"} 1`,
		`pbx_gateway_registered{gateway="trunk1"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}
