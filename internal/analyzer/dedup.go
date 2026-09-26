package analyzer

import (
	"fmt"
	"sync"
	"time"

	"poolwatch/internal/domain"
)

// deduper suppresses repeated alerts with the same key inside a cooldown window.
type deduper struct {
	mu       sync.Mutex
	cooldown time.Duration
	lastSeen map[string]time.Time
}

func newDeduper(cooldown time.Duration) *deduper {
	return &deduper{cooldown: cooldown, lastSeen: make(map[string]time.Time)}
}

func dedupKey(alert domain.Alert) string {
	if app, ok := alert.Details["application"]; ok && app != nil {
		if s := fmt.Sprint(app); s != "" {
			return alert.Code + "|" + s
		}
	}
	return alert.Code
}

// filter returns only alerts whose key has not been emitted within the cooldown.
func (d *deduper) filter(alerts []domain.Alert, now time.Time) []domain.Alert {
	if d == nil || d.cooldown <= 0 || len(alerts) == 0 {
		return alerts
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	for key, seen := range d.lastSeen {
		if now.Sub(seen) >= d.cooldown {
			delete(d.lastSeen, key)
		}
	}

	kept := make([]domain.Alert, 0, len(alerts))
	for _, alert := range alerts {
		key := dedupKey(alert)
		if seen, ok := d.lastSeen[key]; ok && now.Sub(seen) < d.cooldown {
			continue
		}
		d.lastSeen[key] = now
		kept = append(kept, alert)
	}
	return kept
}
