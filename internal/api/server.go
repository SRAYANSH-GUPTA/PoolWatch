package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"poolwatch/internal/config"
	"poolwatch/internal/proxy"
	"poolwatch/internal/store"
)

type Server struct {
	cfg    config.Config
	logger *slog.Logger
	state  *store.State
	proxy  *proxy.Service
	server *http.Server
}

func New(cfg config.Config, logger *slog.Logger, state *store.State, proxyService *proxy.Service) *Server {
	mux := http.NewServeMux()
	service := &Server{
		cfg:    cfg,
		logger: logger,
		state:  state,
		proxy:  proxyService,
		server: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           mux,
			ReadHeaderTimeout: 2 * time.Second,
		},
	}

	mux.HandleFunc("/", service.dashboard)
	mux.HandleFunc("/healthz", service.health)
	mux.HandleFunc("/api/v1/metrics", service.metrics)
	mux.HandleFunc("/api/v1/alerts", service.alerts)
	mux.HandleFunc("/api/v1/status", service.status)
	mux.HandleFunc("/api/v1/proxy/enable", service.enableProxy)
	mux.HandleFunc("/api/v1/proxy/disable", service.disableProxy)

	return service
}

func (s *Server) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.server.Shutdown(shutdownCtx)
	}()

	err := s.server.ListenAndServe()
	if err == nil || err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	status := s.state.Status()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"state":  status,
	})
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.state.Snapshot())
}

func (s *Server) alerts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"alerts": s.state.Alerts(),
	})
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"config": map[string]any{
			"mode":             s.cfg.Mode,
			"http_addr":        s.cfg.HTTPAddr,
			"proxy_listen":     s.cfg.ProxyListenAddr,
			"proxy_upstream":   s.cfg.ProxyUpstreamAddr,
			"collect_interval": s.cfg.CollectInterval.String(),
			"analyze_interval": s.cfg.AnalyzeInterval.String(),
		},
		"state": s.state.Status(),
	})
}

func (s *Server) enableProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := s.proxy.Enable(r.Context()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "enabled"})
}

func (s *Server) disableProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := s.proxy.Disable(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "disabled"})
}

func (s *Server) dashboard(w http.ResponseWriter, _ *http.Request) {
	snapshot := s.state.Snapshot()
	status := s.state.Status()
	page := fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>PoolWatch</title>
<style>
:root { color-scheme: light; --bg:#f4efe7; --panel:#fffdf9; --ink:#1e1d1a; --muted:#6b665f; --accent:#bc4b1f; --accent2:#2660a4; --line:#e6ddd1; }
* { box-sizing:border-box; }
body { margin:0; font-family: "Iowan Old Style", "Palatino Linotype", serif; background:radial-gradient(circle at top, #fff7ef 0, #f4efe7 42%%, #ece4d8 100%%); color:var(--ink); }
.wrap { max-width:1100px; margin:0 auto; padding:32px 20px 60px; }
.hero { display:flex; justify-content:space-between; gap:24px; align-items:flex-end; margin-bottom:24px; }
.hero h1 { margin:0; font-size:56px; line-height:0.95; letter-spacing:-0.04em; }
.hero p { margin:8px 0 0; color:var(--muted); max-width:600px; font-size:18px; }
.grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(220px,1fr)); gap:16px; }
.card { background:rgba(255,253,249,0.84); border:1px solid var(--line); border-radius:22px; padding:18px; backdrop-filter: blur(8px); box-shadow:0 12px 30px rgba(71,43,16,0.07); }
.label { color:var(--muted); font-size:12px; text-transform:uppercase; letter-spacing:0.14em; }
.value { margin-top:10px; font-size:36px; font-weight:700; }
.split { display:grid; grid-template-columns:2fr 1fr; gap:16px; margin-top:16px; }
pre { margin:0; white-space:pre-wrap; font-size:13px; line-height:1.55; font-family: "IBM Plex Mono", monospace; }
@media (max-width: 800px) { .hero, .split { grid-template-columns:1fr; display:block; } .hero h1 { font-size:42px; } .split .card:first-child { margin-bottom:16px; } }
</style>
</head>
<body>
<div class="wrap">
<section class="hero">
<div>
<h1>PoolWatch</h1>
<p>Passive-first connection pool observability for PostgreSQL and pgBouncer with assisted analysis and guarded proxy mode.</p>
</div>
<div class="card">
<div class="label">Mode</div>
<div class="value">%s</div>
</div>
</section>
<section class="grid">
<div class="card"><div class="label">Pool Usage</div><div class="value">%.0f%%</div></div>
<div class="card"><div class="label">Waiting Clients</div><div class="value">%d</div></div>
<div class="card"><div class="label">Active Connections</div><div class="value">%d</div></div>
<div class="card"><div class="label">Proxy Healthy</div><div class="value">%t</div></div>
</section>
<section class="split">
<div class="card"><div class="label">Current Snapshot</div><pre id="snapshot"></pre></div>
<div class="card"><div class="label">Runtime State</div><pre id="state"></pre></div>
</section>
</div>
<script>
document.getElementById("snapshot").textContent = %q;
document.getElementById("state").textContent = %q;
</script>
</body>
</html>`,
		s.cfg.Mode,
		snapshot.Derived.PoolUsage*100,
		snapshot.PgBouncer.WaitingClients+snapshot.Postgres.WaitingConnections,
		snapshot.Postgres.ActiveConnections+snapshot.PgBouncer.ActiveServers,
		snapshot.Proxy.Healthy,
		mustJSON(snapshot),
		mustJSON(status),
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}

func mustJSON(value any) string {
	data, _ := json.MarshalIndent(value, "", "  ")
	return string(data)
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
