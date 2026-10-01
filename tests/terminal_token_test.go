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
		SessionID:    "session-1",
		Exp:          time.Now().Unix() + 60,
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

	noSession := validClaims()
	noSession.SessionID = ""
	if _, err := terminal.ParseToken(secret, mintToken(t, secret, noSession)); err == nil {
		t.Fatal("missing session id should be rejected")
	}

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

func TestParseTokenKeepsAssignment(t *testing.T) {
	secret := "test-secret"
	claims := validClaims()
	claims.Assignment = "S2_BC"

	got, err := terminal.ParseToken(secret, mintToken(t, secret, claims))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Assignment != "S2_BC" {
		t.Fatalf("assignment = %q, want S2_BC", got.Assignment)
	}
}

func TestParseTokenSolutionNeedsOnlyAssignment(t *testing.T) {
	secret := "test-secret"
	claims := terminal.Claims{Assignment: "S2_BC", Solution: true, SessionID: "session-1", Exp: time.Now().Unix() + 60}

	got, err := terminal.ParseToken(secret, mintToken(t, secret, claims))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.Solution || got.Assignment != "S2_BC" {
		t.Fatalf("claims = %+v, want a solution session for S2_BC", got)
	}

	claims.Assignment = ""
	if _, err := terminal.ParseToken(secret, mintToken(t, secret, claims)); err == nil {
		t.Fatal("a solution token without an assignment should be rejected")
	}
}
