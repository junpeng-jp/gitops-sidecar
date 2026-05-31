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
	workers  map[string]*Worker
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
	case model.StateResetting:
		return model.RepoOperationResponse{}, fmt.Errorf("repo reset in progress: %s: %w", req.Name, errConflict)
	}
	bareDir := filepath.Join(c.cfg.WorkDir, req.Name, ".bare")
	if _, err := os.Stat(bareDir); errors.Is(err, os.ErrNotExist) {
		return model.RepoOperationResponse{}, fmt.Errorf("repo not initialised: %s: %w", req.Name, errConflict)
	} else if err != nil {
		return model.RepoOperationResponse{}, fmt.Errorf("stat bare dir %s: %w", bareDir, err)
	}

	w, ok := c.workers[req.Name]
	if !ok {
		return model.RepoOperationResponse{}, fmt.Errorf("repo not found: %s: %w", req.Name, errNotFound)
	}

	switch req.Body.Kind {
	case model.PullKind:
		if req.Body.Ref == "" {
			return model.RepoOperationResponse{}, fmt.Errorf("ref is required for pull: %w", errBadRequest)
		}
		if err := w.Enqueue(model.PullCommand{Name: req.Name, Ref: req.Body.Ref}); err != nil {
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

func (c *GitOpsController) notify(rs model.RepoState, prev model.RepoStateKind) {
	if c.notifier == nil {
		return
	}
	event := model.RepoChangedEvent{
		EventKind:     model.EventKindRepoChanged,
		Name:          rs.Name,
		State:         rs.State,
		PreviousState: prev,
		Ref:           rs.Ref,
		LastUpdatedAt: rs.LastUpdatedAt,
		Error:         rs.Error,
	}
	t := time.NewTimer(3 * time.Second)
	defer t.Stop()
	select {
	case c.notifier.Chan() <- event:
	case <-t.C:
		c.log.Warn("notify: queue full, dropping event", "repo", rs.Name, "state", rs.State)
	}
}

func (c *GitOpsController) Reset(_ context.Context) (model.ResetResponse, error) {
	newStates, prevKinds, err := c.state.LockAll()
	if err != nil {
		return model.ResetResponse{}, fmt.Errorf("reset already in progress: %w", errConflict)
	}

	for _, rs := range newStates {
		c.notify(rs, prevKinds[rs.Name])
	}

	for _, repo := range c.cfg.Repos {
		err := c.workers[repo.Name].Enqueue(model.ResetCommand{Name: repo.Name})
		if err != nil {
			c.log.Error("reset: failed to enqueue reset", "repo", repo.Name, "err", err)
		}
		err = c.workers[repo.Name].Enqueue(model.InitCommand{Name: repo.Name})
		if err != nil {
			c.log.Error("reset: failed to enqueue init", "repo", repo.Name, "err", err)
		}
	}

	return model.ResetResponse{Repos: newStates}, nil
}
