package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"myapphub/server/internal/config"
	"myapphub/server/internal/httpapi"
	"myapphub/server/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0o700); err != nil {
		slog.Error("create log directory", "error", err)
		os.Exit(1)
	}
	logFile, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Error("open log file", "error", err)
		os.Exit(1)
	}
	defer logFile.Close()
	logger := slog.New(slog.NewJSONHandler(logFile, nil))
	repository := store.New(cfg)
	if err := repository.Init(); err != nil {
		logger.Error("initialize store", "error", err)
		os.Exit(1)
	}
	api, err := httpapi.New(cfg, repository, logger)
	if err != nil {
		logger.Error("initialize server", "error", err)
		os.Exit(1)
	}
	server := &http.Server{Addr: cfg.Address, Handler: api.Handler(), ReadHeaderTimeout: 15 * time.Second, ReadTimeout: cfg.UploadTimeout, WriteTimeout: cfg.UploadTimeout, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	cleanupDone := make(chan struct{})
	defer close(cleanupDone)
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				if err := repository.Cleanup(now); err != nil {
					logger.Error("clean expired data", "error", err)
				}
			case <-cleanupDone:
				return
			}
		}
	}()
	go func() {
		logger.Info("server started", "address", cfg.Address)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
