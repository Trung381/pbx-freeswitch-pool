package recordingstore

import (
	"testing"
	"time"
)

func TestValidatedObjectKey(t *testing.T) {
	const prefix = "pbx/recordings"
	valid := "pbx/recordings/2026/08/26/call.wav"
	if got, err := validatedObjectKey(prefix, valid); err != nil || got != valid {
		t.Fatalf("valid key = %q, %v", got, err)
	}
	for _, value := range []string{
		"../etc/passwd",
		"/pbx/recordings/call.wav",
		"pbx/other/call.wav",
		"pbx/recordings/../secret.wav",
	} {
		if _, err := validatedObjectKey(prefix, value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

func TestObjectKey(t *testing.T) {
	got, err := objectKey("pbx/recordings", "d7a03285-4dc5-4ceb-8ba4-c0829e30ef73", ".wav", time.Date(2026, time.August, 26, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	want := "pbx/recordings/2026/08/26/d7a03285-4dc5-4ceb-8ba4-c0829e30ef73.wav"
	if got != want {
		t.Fatalf("object key = %q; want %q", got, want)
	}
}
