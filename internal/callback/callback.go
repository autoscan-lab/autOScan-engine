// Package callback tells the caller that an async grade run has settled.
package callback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// DefaultDelays is the backoff before each retry; the caller still polls, so giving up is safe.
var DefaultDelays = []time.Duration{time.Second, 4 * time.Second, 15 * time.Second}

// Valid reports whether raw is an absolute http(s) URL the engine may call.
func Valid(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

type Notifier struct {
	Client *http.Client
	Secret string
	Delays []time.Duration
}

var errRejected = errors.New("callback rejected")

// Notify POSTs {"run_id": runID} to target with the shared secret, retrying on
// network errors and 5xx responses. The receiver reads the run's state itself.
func (n Notifier) Notify(ctx context.Context, target, runID string) error {
	body, err := json.Marshal(map[string]string{"run_id": runID})
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		err := n.post(ctx, target, body)
		if err == nil || errors.Is(err, errRejected) || attempt >= len(n.Delays) {
			return err
		}
		select {
		case <-time.After(n.Delays[attempt]):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (n Notifier) post(ctx context.Context, target string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %v", errRejected, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Autoscan-Secret", n.Secret)
	resp, err := n.Client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 500:
		return fmt.Errorf("callback failed with status %d", resp.StatusCode)
	default:
		return fmt.Errorf("%w with status %d", errRejected, resp.StatusCode)
	}
}
