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
	"github.com/uneronnik/shortener/internal/httpapi"
	"github.com/uneronnik/shortener/internal/link"
	"github.com/uneronnik/shortener/internal/postgres"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := run(ctx, ":"+port, os.Getenv("DATABASE_URL"), logger); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, addr, databaseURL string, logger *slog.Logger) error {
	cfg, err := loadTimeouts()
	if err != nil {
		return err
	}
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		// Parse errors may contain the connection string, including credentials.
		return errors.New("configure database: invalid DATABASE_URL")
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return errors.New("create database pool: failed")
	}
	defer pool.Close()
	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(connectCtx); err != nil {
		return errors.New("connect database: failed (check availability and credentials)")
	}
	service := link.NewService(postgres.NewRepository(pool), link.GenerateCode)
	return serve(ctx, addr, httpapi.NewHandler(service, baseURL(), httpapi.Options{OperationTimeout: cfg.operation, Logger: logger}), logger)
}

func serve(ctx context.Context, addr string, handler http.Handler, logger *slog.Logger) error {
	server, err := newHTTPServer(addr, handler)
	if err != nil {
		return err
	}
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server", "addr", addr)
		serverErr <- server.ListenAndServe()
	}()
	select {
	case err := <-serverErr:
		return fmt.Errorf("listen HTTP: %w", err)
	case <-ctx.Done():
		logger.Info("stopping HTTP server")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		server.Close()
		return fmt.Errorf("shutdown HTTP: %w", err)
	}
	if err := <-serverErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	logger.Info("HTTP server stopped")
	return nil
}

func newHTTPServer(addr string, handler http.Handler) (*http.Server, error) {
	cfg, err := loadTimeouts()
	if err != nil {
		return nil, err
	}
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: min(5*time.Second, cfg.read),
		ReadTimeout:       cfg.read,
		WriteTimeout:      cfg.write,
		IdleTimeout:       60 * time.Second,
	}, nil
}

func baseURL() string {
	if value := os.Getenv("BASE_URL"); value != "" {
		return value
	}
	return "http://localhost:8080"
}
