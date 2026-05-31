package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/junpeng-jp/gitops-sidecar/internal/client"
	"github.com/junpeng-jp/gitops-sidecar/internal/model"
	"github.com/junpeng-jp/gitops-sidecar/internal/server"
	"github.com/junpeng-jp/gitops-sidecar/internal/storage"
)

var version, commit, date = "dev", "unknown", "unknown"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	logger.Info("starting", "version", version, "commit", commit, "date", date)

	cfg, err := server.LoadConfig()
	if err != nil {
		logger.Error("load config", "err", err)
		os.Exit(1)
	}

	if err := os.RemoveAll(cfg.WorkDir); err != nil {
		logger.Error("wipe workDir", "err", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		logger.Error("create workDir", "err", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.WorkspaceDir, 0o755); err != nil {
		logger.Error("create workspaceDir", "err", err)
		os.Exit(1)
	}

	if cfg.RuntimeDir != "" {
		os.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(cfg.RuntimeDir, "gitconfig"))
		os.Setenv("GIT_SSH_COMMAND", fmt.Sprintf("ssh -F '%s'", filepath.Join(cfg.RuntimeDir, "ssh_config")))
	}

	var state storage.StateStore
	state.Init(cfg.Repos, cfg.WorkspaceDir)

	var notificationWorker *server.NotificationWorker
	if cfg.Notification != nil {
		notifier := client.NewHomeAssistantNotificationWebhook(cfg.Notification.URL)
		notificationWorker = server.NewNotificationWorker(notifier, cfg.Notification, logger)
	}

	gitCtrl := client.ShellGitClient{}
	engine := server.NewWorker(cfg, &state, gitCtrl, notificationWorker, logger)
	gitOpsServer := server.NewServer(cfg, &state, engine, notificationWorker, logger, version, commit, date)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	if notificationWorker != nil {
		go notificationWorker.Run(ctx)
	}
	go engine.Run(ctx)

	go func() {
		logger.Info("server listening", "port", cfg.Port)
		if err := gitOpsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server", "err", err)
		}
	}()

	for _, repo := range cfg.Repos {
		engine.Enqueue(model.InitCommand{Name: repo.Name})
	}

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gitOpsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown", "err", err)
	}
}
