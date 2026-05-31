package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
	"github.com/junpeng-jp/gitops-sidecar/internal/storage"
)

var (
	errNotFound   = errors.New("http 404: not found")
	errConflict   = errors.New("http 409: conflict")
	errBadRequest = errors.New("http 500: bad request")
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

func (c *GitOpsController) GetRepos(ctx context.Context, req model.GetReposRequest) (model.GetReposResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.GetReposResponse{}, err
	}
	return model.GetReposResponse{Repos: c.state.List(req.Limit)}, nil
}

func (c *GitOpsController) GetRepo(ctx context.Context, req model.GetRepoRequest) (model.GetRepoResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.GetRepoResponse{}, err
	}
	rs, ok := c.state.Get(req.Name)
	if !ok {
		return model.GetRepoResponse{}, fmt.Errorf("repo not found: %s: %w", req.Name, errNotFound)
	}
	return model.GetRepoResponse{Repo: rs}, nil
}

func (c *GitOpsController) RepoOperation(ctx context.Context, req model.RepoOperationRequest) (model.RepoOperationResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.RepoOperationResponse{}, err
	}
	rs, ok := c.state.Get(req.Name)
	if !ok {
		return model.RepoOperationResponse{}, fmt.Errorf("repo not found: %s: %w", req.Name, errNotFound)
	}
	if rs.State == model.StateInit {
		return model.RepoOperationResponse{}, fmt.Errorf("repo not initialised: %s: %w", req.Name, errConflict)
	}
	bareDir := filepath.Join(c.cfg.WorkDir, req.Name, ".bare")
	if _, err := os.Stat(bareDir); os.IsNotExist(err) {
		return model.RepoOperationResponse{}, fmt.Errorf("repo not initialised: %s: %w", req.Name, errConflict)
	}

	switch req.Body.Kind {
	case model.PullKind:
		if req.Body.Ref == "" {
			return model.RepoOperationResponse{}, fmt.Errorf("ref is required for pull: %w", errBadRequest)
		}
		c.engine.Enqueue(model.PullCommand{Name: req.Name, Ref: req.Body.Ref})
		rs, _ = c.state.Get(req.Name)
		return model.RepoOperationResponse{Repo: rs}, nil
	default:
		return model.RepoOperationResponse{}, fmt.Errorf("unknown operation kind %q: %w", req.Body.Kind, errBadRequest)
	}
}

func (c *GitOpsController) Reset(ctx context.Context) (model.ResetResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ResetResponse{}, err
	}
	if err := os.RemoveAll(c.cfg.WorkDir); err != nil {
		return model.ResetResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return model.ResetResponse{}, err
	}
	if err := os.RemoveAll(c.cfg.WorkspaceDir); err != nil {
		return model.ResetResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return model.ResetResponse{}, err
	}
	if err := os.MkdirAll(c.cfg.WorkDir, 0o755); err != nil {
		return model.ResetResponse{}, err
	}
	if err := os.MkdirAll(c.cfg.WorkspaceDir, 0o755); err != nil {
		return model.ResetResponse{}, err
	}

	prevStates := make(map[string]model.RepoStateKind, len(c.cfg.Repos))
	for _, repo := range c.cfg.Repos {
		if rs, ok := c.state.Get(repo.Name); ok {
			prevStates[repo.Name] = rs.State
		}
	}

	c.state.SetAll(model.StateInit)

	if c.notifier != nil {
		for _, repo := range c.cfg.Repos {
			rs, _ := c.state.Get(repo.Name)
			c.notifier.Enqueue(model.RepoChangedEvent{
				EventKind:     model.EventKindRepoChanged,
				Name:          repo.Name,
				State:         rs.State,
				PreviousState: prevStates[repo.Name],
				Ref:           rs.Ref,
			})
		}
	}

	for _, repo := range c.cfg.Repos {
		c.engine.Enqueue(model.InitCommand{Name: repo.Name})
	}
	return model.ResetResponse{Success: true}, nil
}
