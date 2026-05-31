package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
	"github.com/junpeng-jp/gitops-sidecar/internal/storage"
)

var (
	errBadRequest = errors.New("http 400: bad request")
	errNotFound   = errors.New("http 404: not found")
	errConflict   = errors.New("http 409: conflict")
)

type GitOpsController struct {
	cfg      *Config
	state    *storage.StateStore
	engine   *Worker
	notifier *NotificationWorker
	log      *slog.Logger
	version  string
	commit   string
	date     string
}

func (c *GitOpsController) Health(_ context.Context) (model.HealthResponse, error) {
	return model.HealthResponse{Status: "ok", Version: c.version, Commit: c.commit, Date: c.date}, nil
}

func (c *GitOpsController) GetRepos(_ context.Context, req model.GetReposRequest) (model.GetReposResponse, error) {
	return model.GetReposResponse{
		Repos: c.state.List(req.Limit),
	}, nil
}

func (c *GitOpsController) GetRepo(_ context.Context, req model.GetRepoRequest) (model.GetRepoResponse, error) {
	rs, err := c.state.Get(req.Name)
	if err != nil {
		return model.GetRepoResponse{}, fmt.Errorf("repo not found: %s: %w", req.Name, errNotFound)
	}
	return model.GetRepoResponse{Repo: rs}, nil
}

func (c *GitOpsController) RepoOperation(_ context.Context, req model.RepoOperationRequest) (model.RepoOperationResponse, error) {
	rs, err := c.state.Get(req.Name)
	if err != nil {
		return model.RepoOperationResponse{}, fmt.Errorf("repo not found: %s: %w", req.Name, errNotFound)
	}
	switch rs.State {
	case model.StateInit:
		return model.RepoOperationResponse{}, fmt.Errorf("repo not initialised: %s: %w", req.Name, errConflict)
	case model.StateSyncing:
		return model.RepoOperationResponse{}, fmt.Errorf("repo sync already in progress: %s: %w", req.Name, errConflict)
	}
	bareDir := filepath.Join(c.cfg.WorkDir, req.Name, ".bare")
	if _, err := os.Stat(bareDir); errors.Is(err, os.ErrNotExist) {
		return model.RepoOperationResponse{}, fmt.Errorf("repo not initialised: %s: %w", req.Name, errConflict)
	} else if err != nil {
		return model.RepoOperationResponse{}, fmt.Errorf("stat bare dir %s: %w", bareDir, err)
	}

	switch req.Body.Kind {
	case model.PullKind:
		if req.Body.Ref == "" {
			return model.RepoOperationResponse{}, fmt.Errorf("ref is required for pull: %w", errBadRequest)
		}
		if err := c.engine.Enqueue(model.PullCommand{Name: req.Name, Ref: req.Body.Ref}); err != nil {
			return model.RepoOperationResponse{}, fmt.Errorf("enqueue pull: %w", err)
		}
		rs, err = c.state.Get(req.Name)
		if err != nil {
			return model.RepoOperationResponse{}, fmt.Errorf("get repo after enqueue: %w", err)
		}
		return model.RepoOperationResponse{Repo: rs}, nil
	default:
		return model.RepoOperationResponse{}, fmt.Errorf("unknown operation kind %q: %w", req.Body.Kind, errBadRequest)
	}
}

const resetLockTimeout = 10 * time.Second

func (c *GitOpsController) Reset(_ context.Context) (model.ResetResponse, error) {
	lockCtx, cancel := context.WithTimeout(context.Background(), resetLockTimeout)
	defer cancel()

	unlock, err := c.engine.AcquireResetLock(lockCtx)
	if err != nil {
		return model.ResetResponse{}, fmt.Errorf("reset: waiting for in-flight operation timed out: %w", err)
	}
	defer unlock()

	// Discard any commands that were pending before the reset.
	c.engine.DrainCommands()

	if err := os.RemoveAll(c.cfg.WorkDir); err != nil {
		return model.ResetResponse{}, fmt.Errorf("remove work dir: %w", err)
	}
	if err := os.RemoveAll(c.cfg.WorkspaceDir); err != nil {
		return model.ResetResponse{}, fmt.Errorf("remove workspace dir: %w", err)
	}
	if err := os.MkdirAll(c.cfg.WorkDir, 0o755); err != nil {
		return model.ResetResponse{}, fmt.Errorf("create work dir: %w", err)
	}
	if err := os.MkdirAll(c.cfg.WorkspaceDir, 0o755); err != nil {
		return model.ResetResponse{}, fmt.Errorf("create workspace dir: %w", err)
	}

	prevStates := make(map[string]model.RepoStateKind, len(c.cfg.Repos))
	for _, repo := range c.cfg.Repos {
		if rs, err := c.state.Get(repo.Name); err == nil {
			prevStates[repo.Name] = rs.State
		}
	}

	c.state.SetAll(model.StateInit)

	if c.notifier != nil {
		for _, repo := range c.cfg.Repos {
			rs, err := c.state.Get(repo.Name)
			if err != nil {
				continue
			}
			if err := c.notifier.Enqueue(model.RepoChangedEvent{
				EventKind:     model.EventKindRepoChanged,
				Name:          repo.Name,
				State:         rs.State,
				PreviousState: prevStates[repo.Name],
				Ref:           rs.Ref,
			}); err != nil {
				c.log.Warn("reset: failed to notify", "repo", repo.Name, "err", err)
			}
		}
	}

	for _, repo := range c.cfg.Repos {
		if err := c.engine.Enqueue(model.InitCommand{Name: repo.Name}); err != nil {
			c.log.Error("reset: failed to enqueue init", "repo", repo.Name, "err", err)
		}
	}
	return model.ResetResponse{Success: true}, nil
}
