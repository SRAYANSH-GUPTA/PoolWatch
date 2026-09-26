package runtime

import (
	"context"
	"log/slog"
	"time"

	"poolwatch/internal/analyzer"
	"poolwatch/internal/api"
	"poolwatch/internal/collector"
	"poolwatch/internal/config"
	"poolwatch/internal/events"
	"poolwatch/internal/notify"
	"poolwatch/internal/persistence"
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
	history   persistence.Store
	notifier  notify.Notifier
}

func New(cfg config.Config, logger *slog.Logger) (*App, error) {
	state := store.New(cfg.HistoryLimit)
	queue := events.New(cfg.EventQueueSize)
	history := persistence.NewNoop()
	if cfg.MongoDBURI != "" {
		connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		mongoStore, err := persistence.NewMongo(connectCtx, cfg)
		if err != nil {
			return nil, err
		}
		history = mongoStore
	}
	proxyService := proxy.New(cfg, logger, queue, state)
	collectorService, err := collector.New(cfg, logger, queue, state, history, proxyService)
	if err != nil {
		return nil, err
	}
	notifier := notify.New(cfg, logger)
	analyzerService := analyzer.New(cfg, logger, queue, state, history, notifier)
	apiServer := api.New(cfg, logger, state, history, proxyService)

	return &App{
		cfg:       cfg,
		logger:    logger,
		collector: collectorService,
		analyzer:  analyzerService,
		api:       apiServer,
		proxy:     proxyService,
		history:   history,
		notifier:  notifier,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = a.history.Close(closeCtx)
	}()
	defer a.notifier.Close()

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
