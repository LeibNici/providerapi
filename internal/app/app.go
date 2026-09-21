package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chenming/providerapi/internal/api"
	"github.com/chenming/providerapi/internal/config"
	"github.com/chenming/providerapi/internal/fsutil"
	"github.com/chenming/providerapi/internal/observability"
	"github.com/chenming/providerapi/internal/plugin"
	"github.com/chenming/providerapi/internal/store"
	"github.com/chenming/providerapi/internal/trace"
)

type App struct {
	Cfg     *config.Config
	Log     *slog.Logger
	Plugins *plugin.Manager
	Store   *store.Store
	Trace   *trace.Service
	Metrics *observability.Metrics
}

func New(cfg *config.Config, log *slog.Logger) (*App, error) {
	if err := fsutil.EnsurePrivateDir(cfg.DataDir()); err != nil {
		return nil, err
	}
	st, err := store.Open(cfg.Storage.SQLite)
	if err != nil {
		return nil, err
	}
	tr, err := trace.New(st, cfg.Storage.Traces, cfg.Debug.Level, cfg.Debug.RetentionD, cfg.Debug.FullRetentionD)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	metrics := observability.New()
	mgr := plugin.NewManager(cfg)
	return &App{Cfg: cfg, Log: log, Plugins: mgr, Store: st, Trace: tr, Metrics: metrics}, nil
}

func (a *App) Run(ctx context.Context) error {
	if err := a.Plugins.StartAll(ctx); err != nil {
		return err
	}
	defer a.Plugins.StopAll()
	for _, in := range a.Plugins.List() {
		_ = a.Store.UpsertPlugin(ctx, in.ID, in.Plugin, in.Manifest.Version, in.Manifest.ProtocolVersion, in.Healthy, in.PID)
		a.Metrics.PluginHealth.WithLabelValues(in.ID).Set(1)
	}

	public := &api.Public{Cfg: a.Cfg, Plugins: a.Plugins, Trace: a.Trace, Metrics: a.Metrics, Log: a.Log}
	admin := &api.Admin{Cfg: a.Cfg, Plugins: a.Plugins, Trace: a.Trace, Metrics: a.Metrics}

	pubSrv := &http.Server{
		Addr:              net.JoinHostPort(a.Cfg.Server.Host, fmt.Sprintf("%d", a.Cfg.Server.Port)),
		Handler:           public.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	admSrv := &http.Server{
		Addr:              net.JoinHostPort(a.Cfg.Admin.Host, fmt.Sprintf("%d", a.Cfg.Admin.Port)),
		Handler:           admin.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		a.Log.Info("public server listening", "addr", pubSrv.Addr)
		if err := pubSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	go func() {
		a.Log.Info("admin server listening", "addr", admSrv.Addr)
		if err := admSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	go a.Trace.RetentionLoop(ctx)

	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = pubSrv.Shutdown(shutdownCtx)
	_ = admSrv.Shutdown(shutdownCtx)
	return a.Store.Close()
}

func Serve(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)
	a, err := New(cfg, log)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return a.Run(ctx)
}
