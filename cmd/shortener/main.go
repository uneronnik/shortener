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
		return fmt.Errorf("create database pool: %w", err)
	}
	defer pool.Close()
	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(connectCtx); err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	service := link.NewService(postgres.NewRepository(pool), link.GenerateCode)
	return serve(ctx, addr, httpapi.NewHandler(service, baseURL()), logger)
}

func serve(ctx context.Context, addr string, handler http.Handler, logger *slog.Logger) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
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

func baseURL() string {
	if value := os.Getenv("BASE_URL"); value != "" {
		return value
	}
	return "http://localhost:8080"
}
