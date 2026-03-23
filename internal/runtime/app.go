package runtime

import (
	"context"
	"log/slog"

	"poolwatch/internal/analyzer"
	"poolwatch/internal/api"
	"poolwatch/internal/collector"
	"poolwatch/internal/config"
	"poolwatch/internal/events"
	"poolwatch/internal/proxy"
	"poolwatch/internal/store"
)

type App struct {
	cfg       config.Config
	logger    *slog.Logger
	collector *collector.Service
	analyzer  *analyzer.Service
	api       *api.Server
	proxy     *proxy.Service
}

func New(cfg config.Config, logger *slog.Logger) (*App, error) {
	state := store.New(cfg.HistoryLimit)
	queue := events.New(cfg.EventQueueSize)
	proxyService := proxy.New(cfg, logger, queue, state)
	collectorService, err := collector.New(cfg, logger, queue, state, proxyService)
	if err != nil {
		return nil, err
	}
	analyzerService := analyzer.New(cfg, logger, queue, state)
	apiServer := api.New(cfg, logger, state, proxyService)

	return &App{
		cfg:       cfg,
		logger:    logger,
		collector: collectorService,
		analyzer:  analyzerService,
		api:       apiServer,
		proxy:     proxyService,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 4)

	go func() {
		errCh <- a.collector.Run(runCtx)
	}()
	go func() {
		errCh <- a.analyzer.Run(runCtx)
	}()
	go func() {
		errCh <- a.api.Run(runCtx)
	}()
	go func() {
		errCh <- a.proxy.Run(runCtx)
	}()

	select {
	case <-ctx.Done():
		a.logger.Info("shutdown requested")
		return nil
	case err := <-errCh:
		if err == nil {
			return nil
		}
		cancel()
		return err
	}
}
