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

func TestLoadGatewayStatus(t *testing.T) {
	out := `    Profile::Gateway-Name	                        Data    	State	Ping Time	IB Calls(F/T)	OB Calls(F/T)
=================================================================================================
     external::oneconnect	sip:oneconnect@125.212.225.105:33333	NOREG	 24.10	0/0	0/0
        external::callbot	sip:FreeSWITCH@103.69.97.113:5080	REGED	  1.56	0/0	0/0
=================================================================================================
2 gateways: Inbound(Failed/Total): 0/0,Outbound(Failed/Total):0/0
`
	if n := LoadGatewayStatus(out); n != 2 {
		t.Fatalf("parsed %d gateways, want 2", n)
	}
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{`pbx_gateway_registered{gateway="callbot"} 1`, `pbx_gateway_registered{gateway="oneconnect"} 0`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}
