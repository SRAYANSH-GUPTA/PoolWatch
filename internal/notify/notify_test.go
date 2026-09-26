package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"poolwatch/internal/config"
	"poolwatch/internal/domain"
)

func TestWebhookFormats(t *testing.T) {
	alerts := []domain.Alert{{Severity: domain.SeverityWarn, Code: "pool_saturation", Message: "pool is 95% used"}}
	tests := []struct {
		name   string
		format string
		check  func(t *testing.T, body []byte)
	}{
		{"generic", FormatGeneric, func(t *testing.T, body []byte) {
			var got []domain.Alert
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode array: %v (%s)", err, body)
			}
			if len(got) != 1 || got[0].Code != "pool_saturation" || got[0].Message != "pool is 95% used" {
				t.Fatalf("unexpected payload: %+v", got)
			}
		}},
		{"slack", FormatSlack, func(t *testing.T, body []byte) {
			var got map[string]string
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode object: %v (%s)", err, body)
			}
			if !strings.Contains(got["text"], "pool is 95% used") || !strings.Contains(got["text"], "pool_saturation") {
				t.Fatalf("slack text missing alert: %q", got["text"])
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bodies := make(chan []byte, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %s", r.Method)
				}
				if ct := r.Header.Get("Content-Type"); ct != "application/json" {
					t.Errorf("content-type = %q", ct)
				}
				b, _ := io.ReadAll(r.Body)
				bodies <- b
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			n := New(config.Config{AlertWebhookURL: srv.URL, AlertWebhookFormat: tc.format}, nil)
			defer n.Close()
			if _, ok := n.(*Webhook); !ok {
				t.Fatalf("New returned %T, want *Webhook", n)
			}
			n.Notify(context.Background(), alerts)

			select {
			case body := <-bodies:
				tc.check(t, body)
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for webhook delivery")
			}
		})
	}
}

func TestNewNoopWhenURLEmpty(t *testing.T) {
	for _, url := range []string{"", "   "} {
		n := New(config.Config{AlertWebhookURL: url}, nil)
		if _, ok := n.(Noop); !ok {
			t.Fatalf("url %q: got %T, want Noop", url, n)
		}
		n.Notify(context.Background(), []domain.Alert{{Code: "x"}})
		n.Close()
	}
}

func TestNotifyAfterCloseDoesNotPanic(t *testing.T) {
	w := NewWebhook("http://127.0.0.1:0", FormatGeneric, nil)
	w.Close()
	w.Close()
	w.Notify(context.Background(), []domain.Alert{{Code: "x"}})
}
