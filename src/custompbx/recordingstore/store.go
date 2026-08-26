// Package recordingstore moves completed FreeSWITCH recordings to private S3
// object storage. Objects are never exposed directly: the authenticated CDR
// endpoint streams them back to a signed-in CustomPBX user.
package recordingstore

import (
	"context"
	"custompbx/callwebhook"
	"custompbx/db"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const defaultPrefix = "pbx/recordings"

const (
	freeSwitchRecordingsDir = "/var/lib/freeswitch/recordings"
	customPBXRecordingsDir  = "/app/recordings"
)

var errNotConfigured = errors.New("recording object storage is not configured")

type config struct {
	endpoint  string
	accessKey string
	secretKey string
	bucket    string
	prefix    string
	secure    bool
}

var (
	clientOnce sync.Once
	client     *minio.Client
	clientErr  error
	startOnce  sync.Once
)

func loadConfig() config {
	cfg := config{
		endpoint:  strings.TrimSpace(os.Getenv("PBX_MINIO_ENDPOINT")),
		accessKey: strings.TrimSpace(os.Getenv("PBX_MINIO_ACCESS_KEY")),
		secretKey: os.Getenv("PBX_MINIO_SECRET_KEY"),
		bucket:    strings.TrimSpace(os.Getenv("PBX_MINIO_BUCKET")),
		prefix:    strings.Trim(strings.TrimSpace(os.Getenv("PBX_MINIO_PREFIX")), "/"),
		secure:    strings.EqualFold(strings.TrimSpace(os.Getenv("PBX_MINIO_SECURE")), "true"),
	}
	if cfg.prefix == "" {
		cfg.prefix = defaultPrefix
	}
	return cfg
}

// Enabled is intentionally all-or-nothing. A partly configured storage
// backend must leave the local spool untouched instead of silently losing a
// recording.
func Enabled() bool {
	cfg := loadConfig()
	return cfg.endpoint != "" && cfg.accessKey != "" && cfg.secretKey != "" && cfg.bucket != ""
}

func getClient() (*minio.Client, config, error) {
	cfg := loadConfig()
	if !Enabled() {
		return nil, cfg, errNotConfigured
	}
	clientOnce.Do(func() {
		client, clientErr = minio.New(cfg.endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.accessKey, cfg.secretKey, ""),
			Secure: cfg.secure,
		})
	})
	return client, cfg, clientErr
}

func objectKey(prefix, callUUID, extension string, now time.Time) (string, error) {
	if strings.Contains(callUUID, "/") || strings.Contains(callUUID, "..") || callUUID == "" {
		return "", errors.New("invalid call UUID")
	}
	if extension == "" {
		extension = ".wav"
	}
	return fmt.Sprintf("%s/%04d/%02d/%02d/%s%s", prefix, now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), callUUID, extension), nil
}

// QueueUpload uploads on a detached worker because the FreeSWITCH ESL event
// loop must never wait on object storage. The local file remains the durable
// retry spool until an upload succeeds.
func QueueUpload(callUUID, localPath string) {
	if !Enabled() || strings.TrimSpace(localPath) == "" {
		return
	}
	if db.GetDB() != nil {
		if _, err := db.GetDB().Exec(`UPDATE cdr SET recording_status = 'pending', recording_error = NULL WHERE uuid = $1::uuid`, callUUID); err != nil {
			log.Printf("recording upload pending metadata update uuid=%s: %v", callUUID, err)
			return
		}
	}
	go upload(callUUID, localPath)
}

// Start reconciles the durable local spool. This covers a MinIO outage or a
// CustomPBX restart between FreeSWITCH finishing the WAV and the asynchronous
// upload succeeding. Existing local recordings are intentionally migrated too.
func Start() {
	if !Enabled() {
		log.Println("recording object storage is disabled; retaining local recordings")
		return
	}
	startOnce.Do(func() {
		go func() {
			retrySpool()
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				retrySpool()
			}
		}()
	})
}

func retrySpool() {
	if db.GetDB() == nil {
		return
	}
	rows, err := db.GetDB().Query(`
SELECT uuid::text, record_path
FROM cdr
WHERE record_path IS NOT NULL
  AND recording_status IN ('local', 'pending', 'failed')
ORDER BY end_stamp ASC
LIMIT 100`)
	if err != nil {
		log.Printf("recording spool reconciliation query failed: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var callUUID, localPath string
		if err := rows.Scan(&callUUID, &localPath); err != nil {
			log.Printf("recording spool reconciliation scan failed: %v", err)
			continue
		}
		QueueUpload(callUUID, localPath)
	}
	if err := rows.Err(); err != nil {
		log.Printf("recording spool reconciliation rows failed: %v", err)
	}
}

func upload(callUUID, localPath string) {
	localPath, err := spoolPath(localPath)
	if err != nil {
		markFailure(callUUID, err)
		return
	}
	fileInfo, err := os.Stat(localPath)
	if err != nil || !fileInfo.Mode().IsRegular() {
		markFailure(callUUID, fmt.Errorf("recording spool unavailable: %w", err))
		return
	}

	client, cfg, err := getClient()
	if err != nil {
		markFailure(callUUID, err)
		return
	}
	key, err := objectKey(cfg.prefix, callUUID, path.Ext(fileInfo.Name()), fileInfo.ModTime())
	if err != nil {
		markFailure(callUUID, err)
		return
	}

	var lastErr error
	for attempt := 1; attempt <= 5; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		_, lastErr = client.FPutObject(ctx, cfg.bucket, key, localPath, minio.PutObjectOptions{ContentType: "audio/wav"})
		cancel()
		if lastErr == nil {
			if err = markUploaded(callUUID, key, fileInfo.Size()); err != nil {
				log.Printf("recording upload metadata failed uuid=%s: %v", callUUID, err)
				return
			}
			if err = os.Remove(localPath); err != nil {
				log.Printf("recording spool cleanup failed uuid=%s: %v", callUUID, err)
			}
			return
		}
		time.Sleep(time.Duration(attempt*attempt) * time.Second)
	}
	markFailure(callUUID, lastErr)
}

// spoolPath maps the same Docker volume from FreeSWITCH's path namespace to
// CustomPBX's mount point. It rejects traversal and arbitrary host paths even
// though CDR fields are internally produced data.
func spoolPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	var relative string
	switch {
	case strings.HasPrefix(raw, freeSwitchRecordingsDir+"/"):
		relative = strings.TrimPrefix(raw, freeSwitchRecordingsDir+"/")
	case strings.HasPrefix(raw, customPBXRecordingsDir+"/"):
		relative = strings.TrimPrefix(raw, customPBXRecordingsDir+"/")
	default:
		return "", errors.New("recording path is outside the shared spool")
	}
	clean := path.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return "", errors.New("invalid recording spool path")
	}
	return path.Join(customPBXRecordingsDir, clean), nil
}

func markUploaded(callUUID, key string, size int64) error {
	_, err := db.GetDB().Exec(`
UPDATE cdr
SET recording_status = 'uploaded', recording_object_key = $2,
    recording_size_bytes = $3, recording_uploaded_at = NOW(), recording_error = NULL
WHERE uuid = $1::uuid`, callUUID, key, size)
	if err == nil {
		var linkedID string
		_ = db.GetDB().QueryRow("SELECT COALESCE(linked_id, uuid::text) FROM cdr WHERE uuid = $1::uuid", callUUID).Scan(&linkedID)
		baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("PBX_RECORDING_API_BASE_URL")), "/")
		if baseURL != "" {
			callwebhook.Publish(callwebhook.Event{
				EventID:      "call.recording_ready:" + callUUID,
				Event:        "call.recording_ready",
				PBXCallID:    linkedID,
				LinkedID:     linkedID,
				LegUUID:      callUUID,
				RecordingURL: baseURL + "/api/v1/pbx/recordings/" + callUUID,
			})
		}
	}
	return err
}

func markFailure(callUUID string, cause error) {
	if db.GetDB() == nil {
		return
	}
	_, err := db.GetDB().Exec(`
UPDATE cdr SET recording_status = 'failed', recording_error = $2
WHERE uuid = $1::uuid`, callUUID, truncateError(cause))
	if err != nil {
		log.Printf("recording upload failure metadata update uuid=%s: %v", callUUID, err)
	}
}

func truncateError(err error) string {
	if err == nil {
		return "unknown upload failure"
	}
	message := err.Error()
	if len(message) > 512 {
		return message[:512]
	}
	return message
}

// ServeHTTP is split out to keep the storage package independent from routes.
func ServeHTTP(ctx context.Context, key string) (*minio.Object, minio.ObjectInfo, error) {
	client, cfg, err := getClient()
	if err != nil {
		return nil, minio.ObjectInfo{}, err
	}
	key, err = validatedObjectKey(cfg.prefix, key)
	if err != nil {
		return nil, minio.ObjectInfo{}, err
	}
	object, err := client.GetObject(ctx, cfg.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, minio.ObjectInfo{}, err
	}
	info, err := object.Stat()
	if err != nil {
		_ = object.Close()
		return nil, minio.ObjectInfo{}, err
	}
	return object, info, nil
}

func validatedObjectKey(prefix, raw string) (string, error) {
	raw, err := url.PathUnescape(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	clean := path.Clean(raw)
	if clean == "." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || clean != raw || !strings.HasPrefix(clean, prefix+"/") {
		return "", errors.New("invalid recording object key")
	}
	return clean, nil
}
