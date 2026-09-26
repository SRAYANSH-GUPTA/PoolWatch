// Package notify delivers analyzer alerts to external sinks such as webhooks.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"poolwatch/internal/config"
	"poolwatch/internal/domain"
)

const (
	FormatGeneric = "generic"
	FormatSlack   = "slack"

	defaultBuffer  = 64
	defaultTimeout = 5 * time.Second
)

// Notifier delivers alerts. Implementations must never block the caller.
type Notifier interface {
	Notify(ctx context.Context, alerts []domain.Alert)
	Close()
}

// New returns a webhook notifier when a URL is configured, otherwise a no-op.
func New(cfg config.Config, logger *slog.Logger) Notifier {
	if strings.TrimSpace(cfg.AlertWebhookURL) == "" {
		return Noop{}
	}
	return NewWebhook(cfg.AlertWebhookURL, cfg.AlertWebhookFormat, logger)
}

// Noop discards all alerts.
type Noop struct{}

func (Noop) Notify(context.Context, []domain.Alert) {}
func (Noop) Close()                                 {}

// Webhook POSTs alert batches as JSON from a single background worker.
type Webhook struct {
	url    string
	format string
	logger *slog.Logger
	client *http.Client
	queue  chan []domain.Alert
	done   chan struct{}
	once   sync.Once
}

func NewWebhook(url, format string, logger *slog.Logger) *Webhook {
	if format == "" {
		format = FormatGeneric
	}
	if logger == nil {
		logger = slog.Default()
	}
	w := &Webhook{
		url:    url,
		format: format,
		logger: logger,
		client: &http.Client{Timeout: defaultTimeout},
		queue:  make(chan []domain.Alert, defaultBuffer),
		done:   make(chan struct{}),
	}
	go w.worker()
	return w
}

// Notify enqueues alerts for delivery; drops (and logs) when the buffer is full.
func (w *Webhook) Notify(_ context.Context, alerts []domain.Alert) {
	if len(alerts) == 0 {
		return
	}
	batch := make([]domain.Alert, len(alerts))
	copy(batch, alerts)
	defer func() {
		// Notify after Close would send on a closed channel; treat as drop.
		if recover() != nil {
			w.logger.Warn("alert webhook closed, dropping alerts", "count", len(batch))
		}
	}()
	select {
	case w.queue <- batch:
	default:
		w.logger.Warn("alert webhook queue full, dropping alerts", "count", len(batch))
	}
}

// Close stops accepting alerts and waits for queued deliveries to finish.
func (w *Webhook) Close() {
	w.once.Do(func() {
		close(w.queue)
		<-w.done
	})
}

func (w *Webhook) worker() {
	defer close(w.done)
	for batch := range w.queue {
		if err := w.send(batch); err != nil {
			w.logger.Warn("alert webhook delivery failed", "error", err, "count", len(batch))
		}
	}
}

func (w *Webhook) send(alerts []domain.Alert) error {
	body, err := w.encode(alerts)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "poolwatch-notifier")
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

func (w *Webhook) encode(alerts []domain.Alert) ([]byte, error) {
	if w.format == FormatSlack {
		return json.Marshal(map[string]string{"text": SlackText(alerts)})
	}
	return json.Marshal(alerts)
}

// SlackText renders alerts as Slack mrkdwn lines prefixed by a severity emoji.
func SlackText(alerts []domain.Alert) string {
	lines := make([]string, 0, len(alerts)+1)
	lines = append(lines, fmt.Sprintf("*PoolWatch*: %d alert(s)", len(alerts)))
	for _, alert := range alerts {
		lines = append(lines, fmt.Sprintf("%s *%s* `%s` %s", severityEmoji(alert.Severity), strings.ToUpper(string(alert.Severity)), alert.Code, alert.Message))
	}
	return strings.Join(lines, "\n")
}

func severityEmoji(severity domain.AlertSeverity) string {
	switch severity {
	case domain.SeverityError:
		return ":rotating_light:"
	case domain.SeverityWarn:
		return ":warning:"
	default:
		return ":information_source:"
	}
}
