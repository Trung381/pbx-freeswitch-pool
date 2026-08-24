package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"custompbx/mainStruct"
	"custompbx/web"
	"encoding/base64"
	"github.com/go-chi/chi/v5"
	"github.com/pion/turn/v4"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTemporaryTURNAuthKey(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	username := "1700003600:42"
	secret := "turn-shared-secret"
	realm := "td1.tekomi.vn"

	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(username))
	password := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	want := turn.GenerateAuthKey(username, realm, password)

	got, ok := temporaryTURNAuthKey(secret, username, realm, now)
	if !ok || !hmac.Equal(got, want) {
		t.Fatalf("temporaryTURNAuthKey() = %x, %t; want %x, true", got, ok, want)
	}
}

func TestTemporaryTURNAuthKeyRejectsInvalidCredentials(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, test := range []struct {
		name     string
		secret   string
		username string
	}{
		{name: "missing secret", username: "1700003600:42"},
		{name: "invalid username", secret: "secret", username: "agent-42"},
		{name: "expired", secret: "secret", username: "1699999999:42"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, ok := temporaryTURNAuthKey(test.secret, test.username, "td1.tekomi.vn", now); ok {
				t.Fatal("temporaryTURNAuthKey() accepted invalid credentials")
			}
		})
	}
}

func TestResolveFileUnderRoot(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "2026")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "call.wav")
	if err := os.WriteFile(file, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveFileUnderRoot(root, "2026", "call.wav")
	if err != nil || got != file {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, parts := range [][]string{{"..", "secret"}, {`..\\secret`}, {"2026", "missing.wav"}} {
		if got, err := resolveFileUnderRoot(root, parts...); err == nil {
			t.Fatalf("unsafe path accepted: %q", got)
		}
	}
}

func TestWebProtectedStaticRoutesRequireValidCookie(t *testing.T) {
	oldLookup := web.HTTPTokenLookup
	defer func() { web.HTTPTokenLookup = oldLookup }()
	web.HTTPTokenLookup = func(token string) (*mainStruct.WebUser, error) {
		if token == "valid" {
			return &mainStruct.WebUser{Id: 1, Login: "admin", GroupId: mainStruct.GetAdminId()}, nil
		}
		return nil, nil
	}

	router := chi.NewRouter()
	configureStaticRoutes(router)

	tests := []struct {
		name   string
		path   string
		token  string
		status int
	}{
		{name: "cdr missing cookie", path: "/cweb/cdr/records/call.wav", status: http.StatusUnauthorized},
		{name: "cdr invalid cookie", path: "/cweb/cdr/records/call.wav", token: "bad", status: http.StatusUnauthorized},
		{name: "avatar missing cookie", path: "/cweb/assets/img/avatar/1.png", status: http.StatusUnauthorized},
		{name: "avatar invalid cookie", path: "/cweb/assets/img/avatar/1.png", token: "bad", status: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.token != "" {
				req.AddCookie(&http.Cookie{Name: "token", Value: tt.token})
			}
			rr := httptest.NewRecorder()

			router.ServeHTTP(rr, req)

			if rr.Code != tt.status {
				t.Fatalf("status=%d, want %d, body=%q", rr.Code, tt.status, rr.Body.String())
			}
		})
	}
}
