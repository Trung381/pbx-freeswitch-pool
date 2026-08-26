package callwebhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"custompbx/cfg"
)

const (
	defaultTimeout   = 5 * time.Second
	defaultQueueSize = 256
)

type Event struct {
	EventID           string `json:"event_id"`
	Event             string `json:"event"`
	PBXID             string `json:"pbx_id,omitempty"`
	SIPDomain         string `json:"sip_domain,omitempty"`
	PBXCallID         string `json:"pbx_call_id"`
	LinkedID          string `json:"linked_id,omitempty"`
	LegUUID           string `json:"leg_uuid"`
	Direction         string `json:"direction,omitempty"`
	BusinessDirection string `json:"business_direction,omitempty"`
	FromNumber        string `json:"from_number,omitempty"`
	ToNumber          string `json:"to_number,omitempty"`
	CustomerNumber    string `json:"customer_number,omitempty"`
	Extension         string `json:"extension,omitempty"`
	Status            string `json:"status,omitempty"`
	StartedAt         string `json:"started_at,omitempty"`
	AnsweredAt        string `json:"answered_at,omitempty"`
	EndedAt           string `json:"ended_at,omitempty"`
	Duration          int64  `json:"duration,omitempty"`
	HangupCause       string `json:"hangup_cause,omitempty"`
	RecordingURL      string `json:"recording_url,omitempty"`
}

type Dispatcher struct {
	url       string
	secret    string
	pbxID     string
	sipDomain string
	client    *http.Client
	queue     chan Event
	seenMu    sync.Mutex
	seen      map[string]time.Time
}

var (
	active   *Dispatcher
	activeMu sync.RWMutex
)

func Start(config cfg.CallWebhook) {
	config = applyEnvironment(config)
	if !config.Enabled || strings.TrimSpace(config.URL) == "" {
		log.Println("Call webhook dispatcher disabled")
		return
	}

	timeout := time.Duration(config.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	queueSize := config.QueueSize
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}

	dispatcher := &Dispatcher{
		url:       strings.TrimSpace(config.URL),
		secret:    config.Secret,
		pbxID:     strings.TrimSpace(os.Getenv("PBX_CALL_WEBHOOK_PBX_ID")),
		sipDomain: strings.TrimSpace(os.Getenv("PBX_CALL_WEBHOOK_SIP_DOMAIN")),
		client:    &http.Client{Timeout: timeout},
		queue:     make(chan Event, queueSize),
		seen:      make(map[string]time.Time),
	}
	activeMu.Lock()
	active = dispatcher
	activeMu.Unlock()

	go dispatcher.run()
	log.Printf("Call webhook dispatcher enabled url=%s", dispatcher.url)
}

func Publish(event Event) {
	activeMu.RLock()
	dispatcher := active
	activeMu.RUnlock()
	if dispatcher == nil {
		return
	}

	event = sanitize(event)
	if event.PBXID == "" {
		event.PBXID = dispatcher.pbxID
	}
	if event.SIPDomain == "" {
		event.SIPDomain = dispatcher.sipDomain
	}
	if event.LinkedID == "" {
		event.LinkedID = event.PBXCallID
	}
	if !dispatcher.markEventSeen(event.EventID, time.Now()) {
		return
	}
	select {
	case dispatcher.queue <- event:
	default:
		log.Printf("Call webhook queue full; dropped event_id=%s event=%s", event.EventID, event.Event)
	}
}

const eventDedupeTTL = 10 * time.Minute

func (d *Dispatcher) markEventSeen(eventID string, now time.Time) bool {
	if eventID == "" {
		return true
	}

	d.seenMu.Lock()
	defer d.seenMu.Unlock()
	if d.seen == nil {
		d.seen = make(map[string]time.Time)
	}
	if seenAt, exists := d.seen[eventID]; exists && now.Sub(seenAt) < eventDedupeTTL {
		return false
	}
	for id, seenAt := range d.seen {
		if now.Sub(seenAt) >= eventDedupeTTL {
			delete(d.seen, id)
		}
	}
	d.seen[eventID] = now
	return true
}

func (d *Dispatcher) run() {
	for event := range d.queue {
		if err := d.deliver(context.Background(), event); err != nil {
			log.Printf("Call webhook delivery failed event_id=%s event=%s error=%v", event.EventID, event.Event, err)
		}
	}
}

func (d *Dispatcher) deliver(ctx context.Context, event Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PBX-Timestamp", timestamp)
	req.Header.Set("X-PBX-Signature", signature(d.secret, timestamp, body))

	response, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return &unexpectedStatusError{status: response.Status}
	}
	return nil
}

func signature(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func sanitize(event Event) Event {
	event.EventID = validUTF8(event.EventID)
	event.Event = validUTF8(event.Event)
	event.PBXID = validUTF8(event.PBXID)
	event.SIPDomain = validUTF8(event.SIPDomain)
	event.PBXCallID = validUTF8(event.PBXCallID)
	event.LinkedID = validUTF8(event.LinkedID)
	event.LegUUID = validUTF8(event.LegUUID)
	event.Direction = validUTF8(event.Direction)
	event.BusinessDirection = validUTF8(event.BusinessDirection)
	event.FromNumber = validUTF8(event.FromNumber)
	event.ToNumber = validUTF8(event.ToNumber)
	event.CustomerNumber = validUTF8(event.CustomerNumber)
	event.Extension = validUTF8(event.Extension)
	event.Status = validUTF8(event.Status)
	event.StartedAt = validUTF8(event.StartedAt)
	event.AnsweredAt = validUTF8(event.AnsweredAt)
	event.EndedAt = validUTF8(event.EndedAt)
	event.HangupCause = validUTF8(event.HangupCause)
	event.RecordingURL = validUTF8(event.RecordingURL)
	return event
}

func validUTF8(value string) string {
	if utf8.ValidString(value) {
		return value
	}
	return strings.ToValidUTF8(value, "�")
}

func applyEnvironment(config cfg.CallWebhook) cfg.CallWebhook {
	if value := strings.TrimSpace(os.Getenv("PBX_CALL_WEBHOOK_URL")); value != "" {
		config.URL = value
		config.Enabled = true
	}
	if value := os.Getenv("PBX_CALL_WEBHOOK_SECRET"); value != "" {
		config.Secret = value
	}
	return config
}

type unexpectedStatusError struct{ status string }

func (e *unexpectedStatusError) Error() string { return "unexpected HTTP status " + e.status }
