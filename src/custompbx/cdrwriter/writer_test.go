package cdrwriter

import "testing"

func TestBuildRecordNormalizesMalformedUTF8(t *testing.T) {
	event := map[string]string{
		eventDirection:                    "inbound",
		eventUUID:                         "84785d5e-62cd-4426-8b63-fb9465a2b4c3",
		"variable_local_ip_v4":            "172.18.0.4",
		"Caller-Caller-ID-Name":           "caller\xd8\x7a",
		"Caller-Caller-ID-Number":         "1002",
		"Caller-Destination-Number":       "0342387314",
		"Caller-Context":                  "default",
		"Caller-Channel-Created-Time":     "1787707901000000",
		"Caller-Channel-Answered-Time":    "1787707907000000",
		"Event-Date-Timestamp":            "1787707912000000",
		"Hangup-Cause":                    "NORMAL_CLEARING",
		"Channel-Read-Codec-Name":         "opus",
		"Channel-Write-Codec-Name":        "opus",
		"variable_sip_hangup_disposition": "send_bye",
	}

	record, ok := buildRecord(event)
	if !ok {
		t.Fatal("expected record")
	}
	if !validUTF8(record.callerIDName) {
		t.Fatalf("caller name is not valid UTF-8: %q", record.callerIDName)
	}
	if record.callerIDName != "caller�z" {
		t.Fatalf("caller name = %q", record.callerIDName)
	}
	if record.duration != 11 || record.billsec != 5 {
		t.Fatalf("duration=%d billsec=%d", record.duration, record.billsec)
	}
}

func TestBuildRecordSkipsBridgedBLeg(t *testing.T) {
	event := map[string]string{
		eventDirection:                "outbound",
		eventUUID:                     "84785d5e-62cd-4426-8b63-fb9465a2b4c3",
		eventOtherUUID:                "2944845b-ce89-4844-94ba-6f636f15e8fa",
		"Caller-Channel-Created-Time": "1787707901000000",
		"Event-Date-Timestamp":        "1787707912000000",
	}
	if _, ok := buildRecord(event); ok {
		t.Fatal("bridged B-leg must not create a second CDR")
	}
}
