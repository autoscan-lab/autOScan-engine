package terminal

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

// The agent mirrors this as MAX_PANES in its /api/terminal route — change both together.
const MaxPanesPerSession = 4

// Claims is the HMAC token payload the agent mints per terminal grant.
type Claims struct {
	RunID        string `json:"run_id"`
	SubmissionID string `json:"submission_id"`
	Student      string `json:"student,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	Panes        int    `json:"panes,omitempty"`
	Exp          int64  `json:"exp"`
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Token format: "payload.sig" — base64url JSON claims plus base64url HMAC-SHA256(secret, payload), minted by the agent.
func ParseToken(secret, token string) (Claims, error) {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || payload == "" || sig == "" {
		return Claims{}, errors.New("malformed token")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return Claims{}, errors.New("invalid token signature")
	}

	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Claims{}, errors.New("malformed token payload")
	}
	var claims Claims
	if err := json.Unmarshal(body, &claims); err != nil {
		return Claims{}, errors.New("malformed token claims")
	}
	if claims.RunID == "" || claims.SubmissionID == "" {
		return Claims{}, errors.New("incomplete token claims")
	}
	if time.Now().Unix() > claims.Exp {
		return Claims{}, errors.New("token expired")
	}

	if claims.SessionID == "" {
		// Legacy single-pane token: synthesize a private session.
		id, err := randomSessionID()
		if err != nil {
			return Claims{}, err
		}
		claims.SessionID = id
		claims.Panes = 1
	} else if !sessionIDPattern.MatchString(claims.SessionID) {
		return Claims{}, errors.New("invalid session id")
	}
	if claims.Panes < 1 {
		claims.Panes = 1
	}
	if claims.Panes > MaxPanesPerSession {
		claims.Panes = MaxPanesPerSession
	}
	return claims, nil
}

func randomSessionID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", errors.New("could not create session id")
	}
	return hex.EncodeToString(buf[:]), nil
}
