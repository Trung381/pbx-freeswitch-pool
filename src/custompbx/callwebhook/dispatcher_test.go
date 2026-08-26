package callwebhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDispatcherDeliversSignedJSON(t *testing.T) {
	t.Parallel()

	received := make(chan Event, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		timestamp := request.Header.Get("X-PBX-Timestamp")
		if got, want := request.Header.Get("X-PBX-Signature"), signature("test-secret", timestamp, body); got != want {
			t.Errorf("signature = %q, want %q", got, want)
		}
		var event Event
		if err := json.Unmarshal(body, &event); err != nil {
			t.Errorf("decode event: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- event
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dispatcher := &Dispatcher{
		url:    server.URL,
		secret: "test-secret",
		client: &http.Client{Timeout: time.Second},
	}
	event := Event{EventID: "call.completed:leg-1", Event: "call.completed", PBXCallID: "call-1", LegUUID: "leg-1"}
	if err := dispatcher.deliver(t.Context(), event); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	select {
	case got := <-received:
		if got != event {
			t.Fatalf("event = %#v, want %#v", got, event)
		}
	case <-time.After(time.Second):
		t.Fatal("webhook was not delivered")
	}
}

func TestSanitizeReplacesInvalidUTF8(t *testing.T) {
	t.Parallel()

	event := sanitize(Event{FromNumber: string([]byte{'1', 0xff, '2'})})
	if !strings.Contains(event.FromNumber, "�") {
		t.Fatalf("FromNumber = %q, expected replacement rune", event.FromNumber)
	}
}

func TestDispatcherRejectsNonSuccessStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	dispatcher := &Dispatcher{url: server.URL, client: &http.Client{Timeout: time.Second}}
	if err := dispatcher.deliver(t.Context(), Event{EventID: "event-1"}); err == nil {
		t.Fatal("expected non-success HTTP status to return an error")
	}
}

func TestDispatcherDeduplicatesEventIDsWithinTTL(t *testing.T) {
	t.Parallel()

	dispatcher := &Dispatcher{seen: make(map[string]time.Time)}
	now := time.Now()
	if !dispatcher.markEventSeen("call.completed:leg-1", now) {
		t.Fatal("first event should be accepted")
	}
	if dispatcher.markEventSeen("call.completed:leg-1", now.Add(time.Second)) {
		t.Fatal("duplicate event should be rejected")
	}
	if !dispatcher.markEventSeen("call.completed:leg-1", now.Add(eventDedupeTTL)) {
		t.Fatal("event should be accepted after the dedupe TTL")
	}
}
