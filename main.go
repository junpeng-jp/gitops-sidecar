package main //nolint:cyclop

import (
	"context"
	"errors"
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

//nolint:gochecknoglobals
var version, commit, date = "dev", "unknown", "unknown"

const shutdownTimeout = 10 * time.Second

func main() { //nolint:funlen
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	logger.Info("starting", "version", version, "commit", commit, "date", date)

	cfg, err := server.LoadConfig()
	if err != nil {
		logger.Error("load config", "err", err)
		os.Exit(1)
	}

	err = os.RemoveAll(cfg.WorkDir)
	if err != nil {
		logger.Error("wipe workDir", "err", err)
		os.Exit(1)
	}
	err = os.MkdirAll(cfg.WorkDir, 0o750)
	if err != nil {
		logger.Error("create workDir", "err", err)
		os.Exit(1)
	}
	err = os.MkdirAll(cfg.WorkspaceDir, 0o750)
	if err != nil {
		logger.Error("create workspaceDir", "err", err)
		os.Exit(1)
	}

	if cfg.RuntimeDir != "" {
		err = os.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(cfg.RuntimeDir, "gitconfig"))
		if err != nil {
			logger.Error("set GIT_CONFIG_GLOBAL", "err", err)
			os.Exit(1)
		}
		err = os.Setenv("GIT_SSH_COMMAND", fmt.Sprintf("ssh -F '%s'", filepath.Join(cfg.RuntimeDir, "ssh_config")))
		if err != nil {
			logger.Error("set GIT_SSH_COMMAND", "err", err)
			os.Exit(1)
		}
	}

	var state storage.StateStore
	state.Init(cfg.Repos, cfg.WorkspaceDir)

	var notificationWorker *server.NotificationWorker
	var notifyCh chan<- model.RepoChangedEvent
	if cfg.Notification != nil {
		notifier := client.NewHomeAssistantNotificationWebhook(cfg.Notification.URL)
		notifyChan := make(chan model.RepoChangedEvent, cfg.Notification.QueueSize)
		notificationWorker = server.NewNotificationWorker(notifier, notifyChan, cfg.Notification, logger)
		notifyCh = notificationWorker.Chan()
	}

	gitCtrl := client.ShellGitClient{}
	workers := make(map[string]*server.Worker, len(cfg.Repos))
	for _, repo := range cfg.Repos {
		cmdCh := make(chan model.Command, repo.CommandQueueSize)
		workers[repo.Name] = server.NewWorker(cfg, repo, cmdCh, &state, gitCtrl, notifyCh, logger)
	}
	gitOpsServer := server.NewServer(cfg, &state, workers, notificationWorker, logger, version, commit, date)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	if notificationWorker != nil {
		go notificationWorker.Run(ctx)
	}
	for _, w := range workers {
		go w.Run(ctx)
	}

	go func() {
		logger.Info("server listening", "port", cfg.Port)
		err := gitOpsServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server", "err", err)
		}
	}()

	for _, repo := range cfg.Repos {
		err := workers[repo.Name].Enqueue(model.InitCommand{Name: repo.Name})
		if err != nil {
			logger.Error("enqueue init command", "repo", repo.Name, "err", err)
		}
	}

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err = gitOpsServer.Shutdown(shutdownCtx)
	if err != nil {
		logger.Error("server shutdown", "err", err)
	}
}
