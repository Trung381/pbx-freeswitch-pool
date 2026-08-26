// Package cdrwriter persists the PBX's authoritative call records.
//
// FreeSWITCH channel variables originate at a SIP boundary and are therefore
// untrusted byte strings. Do not send them directly to PostgreSQL: a malformed
// SIP display name must never make the whole CDR insert fail.
package cdrwriter

import (
	"custompbx/db"
	"database/sql"
	"log"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	eventDirection = "Call-Direction"
	eventUUID      = "Unique-ID"
	eventOtherUUID = "Other-Leg-Unique-ID"
)

type record struct {
	localIP              string
	callerIDName         string
	callerIDNumber       string
	destinationNumber    string
	context              string
	startedAt            time.Time
	answeredAt           sql.NullTime
	endedAt              time.Time
	duration             int64
	billsec              int64
	hangupCause          string
	uuid                 string
	blegUUID             sql.NullString
	accountCode          sql.NullString
	readCodec            sql.NullString
	writeCodec           sql.NullString
	sipHangupDisposition sql.NullString
	ani                  sql.NullString
	recordStereo         sql.NullString
	recordPath           sql.NullString
	recordName           sql.NullString
}

// Persist stores one A-leg CDR. It intentionally owns the FreeSWITCH ->
// PostgreSQL boundary, using parameterized queries and valid UTF-8 only.
func Persist(event map[string]string) {
	record, ok := buildRecord(event)
	if !ok {
		return
	}

	_, err := db.GetDB().Exec(`
INSERT INTO cdr (
  local_ip_v4, caller_id_name, caller_id_number, destination_number, context,
  start_stamp, answer_stamp, end_stamp, duration, billsec, hangup_cause,
  uuid, bleg_uuid, accountcode, read_codec, write_codec,
  sip_hangup_disposition, ani, record_stereo, record_path, record_name
)
SELECT
  $1, $2, $3, $4, $5,
  $6, $7, $8, $9, $10, $11,
  $12::uuid, $13::uuid, $14, $15, $16,
  $17, $18, $19, $20, $21
WHERE NOT EXISTS (SELECT 1 FROM cdr WHERE uuid = $12::uuid)
`, record.localIP, record.callerIDName, record.callerIDNumber, record.destinationNumber, record.context,
		record.startedAt, record.answeredAt, record.endedAt, record.duration, record.billsec, record.hangupCause,
		record.uuid, record.blegUUID, record.accountCode, record.readCodec, record.writeCodec,
		record.sipHangupDisposition, record.ani, record.recordStereo, record.recordPath, record.recordName)
	if err != nil {
		log.Printf("CDR persistence failed uuid=%s: %v", record.uuid, err)
	}
}

func buildRecord(event map[string]string) (record, bool) {
	// mod_cdr_pg_csv with legs=a records the originating (inbound) leg only.
	// Preserve that behavior and record a single row for bridged calls.
	if value(event, eventDirection) != "inbound" && value(event, eventOtherUUID) != "" {
		return record{}, false
	}

	callUUID := value(event, eventUUID)
	if _, err := uuid.Parse(callUUID); err != nil {
		return record{}, false
	}

	startedAt := eventTime(value(event, "Caller-Channel-Created-Time"))
	endedAt := eventTime(value(event, "Event-Date-Timestamp"))
	if startedAt.IsZero() || endedAt.IsZero() {
		return record{}, false
	}
	answeredAt := eventTime(value(event, "Caller-Channel-Answered-Time"))
	duration := int64(0)
	if endedAt.After(startedAt) {
		duration = int64(endedAt.Sub(startedAt).Seconds())
	}
	billsec := int64(0)
	if !answeredAt.IsZero() && endedAt.After(answeredAt) {
		billsec = int64(endedAt.Sub(answeredAt).Seconds())
	}

	return record{
		localIP:              localIP(event),
		callerIDName:         value(event, "Caller-Caller-ID-Name"),
		callerIDNumber:       value(event, "Caller-Caller-ID-Number"),
		destinationNumber:    value(event, "Caller-Destination-Number"),
		context:              value(event, "Caller-Context"),
		startedAt:            startedAt,
		answeredAt:           nullableTime(answeredAt),
		endedAt:              endedAt,
		duration:             duration,
		billsec:              billsec,
		hangupCause:          value(event, "Hangup-Cause"),
		uuid:                 callUUID,
		blegUUID:             nullableUUID(value(event, eventOtherUUID)),
		accountCode:          nullable(value(event, "variable_accountcode")),
		readCodec:            nullable(value(event, "Channel-Read-Codec-Name")),
		writeCodec:           nullable(value(event, "Channel-Write-Codec-Name")),
		sipHangupDisposition: nullable(value(event, "variable_sip_hangup_disposition")),
		ani:                  nullable(value(event, "variable_ani")),
		recordStereo:         nullable(value(event, "variable_RECORD_STEREO")),
		recordPath:           nullable(firstValue(event, "variable_record_path", "variable_recording_url")),
		recordName:           nullable(value(event, "variable_record_name")),
	}, true
}

func value(event map[string]string, key string) string {
	return strings.ToValidUTF8(event[key], "�")
}

func firstValue(event map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := value(event, key); value != "" {
			return value
		}
	}
	return ""
}

func localIP(event map[string]string) string {
	for _, key := range []string{"variable_local_ip_v4", "FreeSWITCH-IPv4", "Caller-Network-Addr"} {
		if ip := net.ParseIP(value(event, key)); ip != nil {
			return ip.String()
		}
	}
	return "127.0.0.1"
}

func eventTime(raw string) time.Time {
	epoch, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || epoch <= 0 {
		return time.Time{}
	}
	if epoch > 1_000_000_000_000 {
		return time.Unix(0, epoch*1_000).UTC()
	}
	return time.Unix(epoch, 0).UTC()
}

func nullable(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func nullableUUID(value string) sql.NullString {
	if _, err := uuid.Parse(value); err != nil {
		return sql.NullString{}
	}
	return nullable(value)
}

func nullableTime(value time.Time) sql.NullTime {
	return sql.NullTime{Time: value, Valid: !value.IsZero()}
}

// validUTF8 is retained as a small assertion point for tests and documents
// the invariant enforced before every PostgreSQL parameter is sent.
func validUTF8(value string) bool { return utf8.ValidString(value) }
