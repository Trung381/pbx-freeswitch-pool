package callwebhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"custompbx/cfg"
	"custompbx/logx"
	"custompbx/tracing"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
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
	InboxID           int64  `json:"inbox_id,omitempty"`
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

	// TraceContext optionally carries the span of the producer so the async
	// delivery becomes its child. Never serialised.
	TraceContext context.Context `json:"-"`
}

type Dispatcher struct {
	url              string
	secret           string
	pbxID            string
	sipDomain        string
	inboxByExtension map[string]int64
	client           *http.Client
	queue            chan Event
	seenMu           sync.Mutex
	seen             map[string]time.Time
}

var logger = logx.Component("callwebhook")

var (
	active   *Dispatcher
	activeMu sync.RWMutex
)

func Start(config cfg.CallWebhook) {
	config = applyEnvironment(config)
	if !config.Enabled || strings.TrimSpace(config.URL) == "" {
		logger.Info("call webhook dispatcher disabled")
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
		url:              strings.TrimSpace(config.URL),
		secret:           config.Secret,
		pbxID:            strings.TrimSpace(os.Getenv("PBX_CALL_WEBHOOK_PBX_ID")),
		sipDomain:        strings.TrimSpace(os.Getenv("PBX_CALL_WEBHOOK_SIP_DOMAIN")),
		inboxByExtension: parseInboxByExtension(os.Getenv("PBX_CALL_WEBHOOK_INBOX_BY_EXTENSION")),
		client:           &http.Client{Timeout: timeout, Transport: otelhttp.NewTransport(http.DefaultTransport)},
		queue:            make(chan Event, queueSize),
		seen:             make(map[string]time.Time),
	}
	activeMu.Lock()
	active = dispatcher
	activeMu.Unlock()

	go dispatcher.run()
	logger.Info("call webhook dispatcher enabled", "url", dispatcher.url)
}

func Publish(event Event) {
	activeMu.RLock()
	dispatcher := active
	activeMu.RUnlock()
	if dispatcher == nil {
		return
	}

	event = sanitize(event)
	if event.Extension == "" {
		event.Extension = extensionCandidate(event.FromNumber, event.ToNumber)
	}
	if event.PBXID == "" {
		event.PBXID = dispatcher.pbxID
	}
	if event.SIPDomain == "" {
		event.SIPDomain = dispatcher.sipDomain
	}
	if event.InboxID == 0 && event.Extension != "" {
		event.InboxID = dispatcher.inboxByExtension[event.Extension]
	}
	// Older Chatwoot deployments do not consume inbox_id and resolve a phone
	// inbox by sip_domain before trying the extension. Use a non-channel routing
	// key for explicitly mapped extensions so they can fall through safely.
	if event.InboxID > 0 {
		event.SIPDomain = "inbox-" + strconv.FormatInt(event.InboxID, 10)
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
		logger.Warn("call webhook queue full; event dropped", "event_id", event.EventID, "event", event.Event)
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
			logger.Error("call webhook delivery failed", "event_id", event.EventID, "event", event.Event, "error", err)
		}
	}
}

func (d *Dispatcher) deliver(ctx context.Context, event Event) (err error) {
	if event.TraceContext != nil {
		ctx = event.TraceContext
	}
	ctx, span := tracing.StartCall(ctx, event.PBXCallID, "callwebhook.deliver",
		attribute.String("event_id", event.EventID), attribute.String("event", event.Event),
		attribute.String("leg_uuid", event.LegUUID))
	defer span.End()
	defer func() { tracing.RecordError(span, err) }()
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
	span.SetAttributes(attribute.Int("http.response.status_code", response.StatusCode))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return &unexpectedStatusError{status: response.Status}
	}
	logger.InfoContext(ctx, "call webhook delivered", "event_id", event.EventID, "event", event.Event,
		"extension", event.Extension, "inbox_id", event.InboxID, "status", response.Status)
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

func parseInboxByExtension(value string) map[string]int64 {
	result := make(map[string]int64)
	for _, entry := range strings.Split(value, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), ":", 2)
		if len(parts) != 2 {
			continue
		}
		extension := strings.TrimSpace(parts[0])
		inboxID, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if extension == "" || err != nil || inboxID <= 0 {
			continue
		}
		result[extension] = inboxID
	}
	return result
}

func extensionCandidate(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if len(value) < 2 || len(value) > 6 {
			continue
		}
		if _, err := strconv.ParseUint(value, 10, 32); err == nil {
			return value
		}
	}
	return ""
}

type unexpectedStatusError struct{ status string }

func (e *unexpectedStatusError) Error() string { return "unexpected HTTP status " + e.status }
