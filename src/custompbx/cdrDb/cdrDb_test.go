package cdrDb

import "testing"

func TestRecordingRoute(t *testing.T) {
	tests := []struct {
		name       string
		recordPath string
		servedRoot string
		want       string
		ok         bool
	}{
		{
			name:       "maps FreeSWITCH shared-volume path",
			recordPath: "/var/lib/freeswitch/recordings/call.wav",
			servedRoot: "/app/recordings",
			want:       "./cdr/records/call.wav",
			ok:         true,
		},
		{
			name:       "maps CustomPBX path",
			recordPath: "/app/recordings/2026/call.wav",
			servedRoot: "/app/recordings",
			want:       "./cdr/records/2026/call.wav",
			ok:         true,
		},
		{
			name:       "maps portable relative path",
			recordPath: "call.wav",
			servedRoot: "/app/recordings",
			want:       "./cdr/records/call.wav",
			ok:         true,
		},
		{
			name:       "rejects traversal",
			recordPath: "../../etc/passwd",
			servedRoot: "/app/recordings",
			ok:         false,
		},
		{
			name:       "rejects unknown absolute path",
			recordPath: "/tmp/call.wav",
			servedRoot: "/app/recordings",
			ok:         false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := recordingRoute(test.recordPath, test.servedRoot)
			if ok != test.ok || got != test.want {
				t.Fatalf("recordingRoute(%q, %q) = (%q, %t), want (%q, %t)", test.recordPath, test.servedRoot, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestManagedCDRColumnsIncludePlaybackColumn(t *testing.T) {
	found := false
	for _, column := range managedCDRColumns {
		if column == "record_path" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("managed CDR projection must expose record_path to the CDR UI")
	}
}
