package analyzer

import (
	"testing"
	"time"

	"poolwatch/internal/domain"
)

func TestDeduperFilter(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := domain.Alert{Code: "pool_saturation"}
	appA := domain.Alert{Code: "leak", Details: map[string]any{"application": "api"}}
	appB := domain.Alert{Code: "leak", Details: map[string]any{"application": "worker"}}

	type step struct {
		at     time.Duration
		alerts []domain.Alert
		want   int
	}
	tests := []struct {
		name     string
		cooldown time.Duration
		steps    []step
	}{
		{"suppressed within cooldown", time.Minute, []step{{0, []domain.Alert{a}, 1}, {30 * time.Second, []domain.Alert{a}, 0}}},
		{"re-emitted after cooldown", time.Minute, []step{{0, []domain.Alert{a}, 1}, {time.Minute, []domain.Alert{a}, 1}}},
		{"different applications not suppressed", time.Minute, []step{{0, []domain.Alert{appA}, 1}, {time.Second, []domain.Alert{appB, appA}, 1}}},
		{"duplicates in same batch collapsed", time.Minute, []step{{0, []domain.Alert{a, a}, 1}}},
		{"zero cooldown passes everything", 0, []step{{0, []domain.Alert{a, a}, 2}, {time.Second, []domain.Alert{a}, 1}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newDeduper(tc.cooldown)
			for i, s := range tc.steps {
				got := d.filter(s.alerts, base.Add(s.at))
				if len(got) != s.want {
					t.Fatalf("step %d: got %d alerts, want %d", i, len(got), s.want)
				}
			}
		})
	}
}

func TestDeduperNilSafe(t *testing.T) {
	var d *deduper
	alerts := []domain.Alert{{Code: "x"}, {Code: "x"}}
	if got := d.filter(alerts, time.Now()); len(got) != 2 {
		t.Fatalf("nil deduper: got %d, want 2", len(got))
	}
}
