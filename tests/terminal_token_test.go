package tests

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/autoscan-lab/autoscan-engine/internal/terminal"
)

func mintToken(t *testing.T, secret string, claims terminal.Claims) string {
	t.Helper()
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func validClaims() terminal.Claims {
	return terminal.Claims{
		RunID:        "abc123",
		SubmissionID: "Student_269539_assignsubmission_file",
		Exp:          time.Now().Unix() + 60,
	}
}

func TestParseTokenLegacySynthesizesSession(t *testing.T) {
	secret := "test-secret"
	claims := validClaims()
	claims.Panes = 7 // ignored without a session id

	got, err := terminal.ParseToken(secret, mintToken(t, secret, claims))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.SessionID == "" {
		t.Fatal("legacy token should get a synthesized session id")
	}
	if got.Panes != 1 {
		t.Fatalf("legacy token panes = %d, want 1", got.Panes)
	}
}

func TestParseTokenClampsPanes(t *testing.T) {
	secret := "test-secret"
	for _, tc := range []struct{ in, want int }{
		{0, 1},
		{1, 1},
		{3, 3},
		{9, terminal.MaxPanesPerSession},
	} {
		claims := validClaims()
		claims.SessionID = "session-1"
		claims.Panes = tc.in
		got, err := terminal.ParseToken(secret, mintToken(t, secret, claims))
		if err != nil {
			t.Fatalf("parse panes=%d: %v", tc.in, err)
		}
		if got.Panes != tc.want {
			t.Fatalf("panes=%d clamped to %d, want %d", tc.in, got.Panes, tc.want)
		}
		if got.SessionID != "session-1" {
			t.Fatalf("session id changed: %q", got.SessionID)
		}
	}
}

func TestParseTokenRejects(t *testing.T) {
	secret := "test-secret"

	badSession := validClaims()
	badSession.SessionID = "a/b"
	if _, err := terminal.ParseToken(secret, mintToken(t, secret, badSession)); err == nil {
		t.Fatal("slash in session id should be rejected")
	}

	longSession := validClaims()
	longSession.SessionID = strings.Repeat("a", 65)
	if _, err := terminal.ParseToken(secret, mintToken(t, secret, longSession)); err == nil {
		t.Fatal("over-long session id should be rejected")
	}

	expired := validClaims()
	expired.Exp = time.Now().Unix() - 1
	if _, err := terminal.ParseToken(secret, mintToken(t, secret, expired)); err == nil {
		t.Fatal("expired token should be rejected")
	}

	if _, err := terminal.ParseToken(secret, mintToken(t, "other-secret", validClaims())); err == nil {
		t.Fatal("wrong signature should be rejected")
	}
}
