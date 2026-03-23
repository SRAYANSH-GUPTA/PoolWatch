package proxy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"poolwatch/internal/config"
	"poolwatch/internal/domain"
	"poolwatch/internal/events"
	"poolwatch/internal/store"
)

type Service struct {
	cfg              config.Config
	logger           *slog.Logger
	queue            *events.Queue
	state            *store.State
	listenerMu       sync.Mutex
	listener         net.Listener
	enabled          atomic.Bool
	healthy          atomic.Bool
	openConnections  atomic.Int64
	accepted         atomic.Int64
	closed           atomic.Int64
	ingressBytes     atomic.Int64
	egressBytes      atomic.Int64
	lastError        atomic.Value
	sessionLatencies latencyWindow
}

func New(cfg config.Config, logger *slog.Logger, queue *events.Queue, state *store.State) *Service {
	service := &Service{
		cfg:    cfg,
		logger: logger,
		queue:  queue,
		state:  state,
	}
	service.enabled.Store(cfg.Mode == config.ModeProxy)
	service.healthy.Store(true)
	service.lastError.Store("")
	return service
}

func (s *Service) Run(ctx context.Context) error {
	if s.cfg.Mode != config.ModeProxy {
		return nil
	}
	return s.listen(ctx)
}

func (s *Service) Metrics() domain.ProxyMetrics {
	lastError, _ := s.lastError.Load().(string)
	return domain.ProxyMetrics{
		Enabled:             s.enabled.Load(),
		Healthy:             s.healthy.Load(),
		OpenConnections:     s.openConnections.Load(),
		AcceptedConnections: s.accepted.Load(),
		ClosedConnections:   s.closed.Load(),
		IngressBytes:        s.ingressBytes.Load(),
		EgressBytes:         s.egressBytes.Load(),
		P95SessionLatency:   s.sessionLatencies.Percentile(0.95),
		LastError:           lastError,
	}
}

func (s *Service) Enable(ctx context.Context) error {
	s.enabled.Store(true)
	s.healthy.Store(true)
	if s.cfg.Mode != config.ModeProxy {
		go func() {
			if err := s.listen(ctx); err != nil {
				s.logger.Warn("proxy listener stopped", "error", err)
			}
		}()
	}
	return nil
}

func (s *Service) Disable() error {
	s.enabled.Store(false)
	s.healthy.Store(false)
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Service) listen(ctx context.Context) error {
	s.listenerMu.Lock()
	if s.listener != nil {
		s.listenerMu.Unlock()
		return nil
	}
	ln, err := net.Listen("tcp", s.cfg.ProxyListenAddr)
	if err != nil {
		s.listenerMu.Unlock()
		return err
	}
	s.listener = ln
	s.listenerMu.Unlock()
	defer func() {
		s.listenerMu.Lock()
		s.listener = nil
		s.listenerMu.Unlock()
	}()

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			if !s.enabled.Load() {
				return nil
			}
			s.lastError.Store(err.Error())
			return err
		}
		if !s.enabled.Load() {
			_ = conn.Close()
			continue
		}
		if s.openConnections.Load() >= int64(s.cfg.ProxyConnectionLimit) {
			s.trip("proxy connection limit exceeded")
			_ = conn.Close()
			continue
		}
		s.accepted.Add(1)
		s.openConnections.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Service) handleConn(client net.Conn) {
	start := time.Now()
	defer func() {
		s.openConnections.Add(-1)
		s.closed.Add(1)
		s.sessionLatencies.Add(time.Since(start))
		_ = client.Close()
	}()

	upstream, err := net.DialTimeout("tcp", s.cfg.ProxyUpstreamAddr, 2*time.Second)
	if err != nil {
		s.trip(err.Error())
		return
	}
	defer upstream.Close()

	done := make(chan struct{}, 2)
	go s.pipe(upstream, client, &s.ingressBytes, done)
	go s.pipe(client, upstream, &s.egressBytes, done)
	<-done
	<-done

	if p95 := s.sessionLatencies.Percentile(0.95); p95 > s.cfg.ProxyLatencyLimit {
		s.trip("proxy latency limit exceeded")
	}
	if s.queue.Len() > s.cfg.ProxyQueueLimit {
		s.trip("proxy queue limit exceeded")
	}
}

func (s *Service) pipe(dst net.Conn, src net.Conn, counter *atomic.Int64, done chan<- struct{}) {
	written, err := io.Copy(dst, src)
	counter.Add(written)
	if err != nil {
		s.lastError.Store(err.Error())
	}
	done <- struct{}{}
}

func (s *Service) trip(reason string) {
	s.lastError.Store(reason)
	s.healthy.Store(false)
	s.enabled.Store(false)
	s.state.SetProxyStatus(false, false)
	s.queue.Publish(domain.Event{
		Type:      "proxy_trip",
		Timestamp: time.Now().UTC(),
		Values: map[string]any{
			"reason": reason,
		},
	})
	s.listenerMu.Lock()
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.listenerMu.Unlock()
}

type latencyWindow struct {
	mu      sync.Mutex
	samples []time.Duration
}

func (w *latencyWindow) Add(value time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.samples = append(w.samples, value)
	if len(w.samples) > 512 {
		w.samples = w.samples[len(w.samples)-512:]
	}
}

func (w *latencyWindow) Percentile(ratio float64) time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.samples) == 0 {
		return 0
	}
	samples := make([]time.Duration, len(w.samples))
	copy(samples, w.samples)
	sortDurations(samples)
	index := int(float64(len(samples)-1) * ratio)
	if index < 0 {
		index = 0
	}
	if index >= len(samples) {
		index = len(samples) - 1
	}
	return samples[index]
}

func sortDurations(values []time.Duration) {
	for i := 1; i < len(values); i++ {
		j := i
		for j > 0 && values[j-1] > values[j] {
			values[j-1], values[j] = values[j], values[j-1]
			j--
		}
	}
}
