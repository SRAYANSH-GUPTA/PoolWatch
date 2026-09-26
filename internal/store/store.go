package store

import (
	"sync"

	"poolwatch/internal/domain"
)

type State struct {
	mu            sync.RWMutex
	current       domain.Snapshot
	history       []domain.Snapshot
	alerts        []domain.Alert
	queueDepth    int
	droppedEvents uint64
	proxyEnabled  bool
	proxyHealthy  bool
	alertCounts   map[string]uint64
}

func New(historyLimit int) *State {
	return &State{
		history: make([]domain.Snapshot, 0, historyLimit),
		alerts:  make([]domain.Alert, 0, historyLimit),
	}
}

func (s *State) UpdateSnapshot(snapshot domain.Snapshot, historyLimit int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = snapshot
	s.history = append(s.history, snapshot)
	if len(s.history) > historyLimit {
		s.history = s.history[len(s.history)-historyLimit:]
	}
}

func (s *State) AddAlert(alert domain.Alert, historyLimit int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alerts = append(s.alerts, alert)
	if len(s.alerts) > historyLimit {
		s.alerts = s.alerts[len(s.alerts)-historyLimit:]
	}
	if s.alertCounts == nil {
		s.alertCounts = make(map[string]uint64)
	}
	s.alertCounts[alert.Code]++
}

// AlertCounts returns a copy of the monotonic per-code alert counters.
func (s *State) AlertCounts() map[string]uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]uint64, len(s.alertCounts))
	for code, count := range s.alertCounts {
		result[code] = count
	}
	return result
}

func (s *State) SetQueue(depth int, dropped uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queueDepth = depth
	s.droppedEvents = dropped
}

func (s *State) SetProxyStatus(enabled, healthy bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proxyEnabled = enabled
	s.proxyHealthy = healthy
}

func (s *State) Snapshot() domain.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *State) History() []domain.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]domain.Snapshot, len(s.history))
	copy(result, s.history)
	return result
}

func (s *State) Alerts() []domain.Alert {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]domain.Alert, len(s.alerts))
	copy(result, s.alerts)
	return result
}

func (s *State) Status() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{
		"queue_depth":    s.queueDepth,
		"dropped_events": s.droppedEvents,
		"proxy_enabled":  s.proxyEnabled,
		"proxy_healthy":  s.proxyHealthy,
		"history_points": len(s.history),
		"alert_count":    len(s.alerts),
	}
}
