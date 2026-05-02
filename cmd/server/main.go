package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/Macszym/stock-market-simulator/internal/api"
	"github.com/Macszym/stock-market-simulator/internal/config"
	"github.com/Macszym/stock-market-simulator/internal/service"
	"github.com/Macszym/stock-market-simulator/internal/storage"
	"github.com/Macszym/stock-market-simulator/migrations"
)

const shutdownTimeout = 5 * time.Second

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	if err := run(); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	poolCfg, err := pgxpool.ParseConfig(cfg.DB.DSN())
	if err != nil {
		return fmt.Errorf("parse db dsn: %w", err)
	}
	// MaxConns=10 leaves headroom against postgres default max_connections=100
	// (N replicas * 10 + admin/migrations/tests). MinConns=2 keeps warm
	// connections so the first request after startup does not pay auth latency.
	poolCfg.MinConns = 2
	poolCfg.MaxConns = 10

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("init db pool: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}
	slog.Info("db connected", "host", cfg.DB.Host, "name", cfg.DB.Name)

	if err := runMigrations(pool); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	slog.Info("migrations applied")

	repo := storage.NewPostgres(pool)
	svc := service.NewService(repo, slog.Default())
	// /chaos exits hard via os.Exit(1) instead of routing through stop()'s
	// graceful drain. Defers are skipped on purpose - this simulates the
	// unannounced node failure the HA setup is meant to survive. SIGTERM and
	// SIGINT keep the graceful path via stop().
	srv := api.NewServer(svc, slog.Default(), func() {
		slog.Warn("chaos endpoint invoked, exiting")
		os.Exit(1)
	})

	httpSrv := &http.Server{
		Addr:              ":" + cfg.HTTP.Port,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server starting", "addr", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		return nil
	case <-ctx.Done():
		slog.Info("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		slog.Info("server stopped cleanly")
		return nil
	}
}

func runMigrations(pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	if err := goose.Up(db, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
