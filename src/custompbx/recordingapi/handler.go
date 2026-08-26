// Package recordingapi exposes recordings only to the Chatwoot backend.
package recordingapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"custompbx/db"
	"custompbx/recordingstore"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

const maxClockSkew = time.Minute

var usedNonces = struct {
	sync.Mutex
	values map[string]time.Time
}{values: map[string]time.Time{}}

// Serve verifies a signed server-to-server request and streams the private
// MinIO object. Browsers never receive the shared secret.
func Serve(w http.ResponseWriter, r *http.Request, callUUID string) {
	if !validRequest(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var objectKey string
	err := db.GetDB().QueryRow(`SELECT recording_object_key FROM cdr WHERE uuid = $1::uuid AND recording_status = 'uploaded'`, callUUID).Scan(&objectKey)
	if err != nil || objectKey == "" {
		http.Error(w, "recording not found", http.StatusNotFound)
		return
	}
	object, info, err := recordingstore.ServeHTTP(r.Context(), objectKey)
	if err != nil {
		http.Error(w, "recording unavailable", http.StatusBadGateway)
		return
	}
	defer object.Close()
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, path.Base(objectKey), info.LastModified, object)
}

func validRequest(r *http.Request) bool {
	secret := os.Getenv("PBX_RECORDING_FETCH_SECRET")
	if len(secret) < 32 {
		return false
	}
	timestamp := r.Header.Get("X-Chatwoot-Timestamp")
	nonce := r.Header.Get("X-Chatwoot-Nonce")
	received := r.Header.Get("X-Chatwoot-Signature")
	parsed, err := time.Parse(time.RFC3339, timestamp)
	if err != nil || nonce == "" || len(nonce) < 16 || time.Since(parsed).Abs() > maxClockSkew {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.Join([]string{timestamp, nonce, r.Method, r.URL.EscapedPath()}, ".")))
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if len(received) != len(expected) || !hmac.Equal([]byte(received), []byte(expected)) {
		return false
	}
	return claimNonce(nonce, time.Now())
}

func claimNonce(nonce string, now time.Time) bool {
	usedNonces.Lock()
	defer usedNonces.Unlock()
	for value, createdAt := range usedNonces.values {
		if now.Sub(createdAt) > maxClockSkew {
			delete(usedNonces.values, value)
		}
	}
	if _, exists := usedNonces.values[nonce]; exists {
		return false
	}
	usedNonces.values[nonce] = now
	return true
}

var ErrInvalidRecordingURL = errors.New("recording api base URL is not configured")
