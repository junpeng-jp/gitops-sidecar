package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"log/slog"

	"github.com/junpeng-jp/gitops-sidecar/internal/client"
	"github.com/junpeng-jp/gitops-sidecar/internal/model"
	"github.com/junpeng-jp/gitops-sidecar/internal/storage"
)

var (
	errCloneFailed  = errors.New("clone failed")
	errFetchFailed  = errors.New("fetch error")
	errPruneFailed  = errors.New("prune error")
	errAddFailed    = errors.New("add error")
	errSwapFailed   = errors.New("swap failed")
	errBadSignature = errors.New("bad signature")
)

type Worker struct {
	cfg      *Config
	state    *storage.StateStore
	git      client.GitClient
	notifier *NotificationWorker
	log      *slog.Logger
	cmdCh    chan model.Command
}

func NewWorker(cfg *Config, state *storage.StateStore, git client.GitClient, notifier *NotificationWorker, log *slog.Logger) *Worker {
	return &Worker{
		cfg:      cfg,
		state:    state,
		git:      git,
		notifier: notifier,
		log:      log,
		cmdCh:    make(chan model.Command, 16),
	}
}

func (e *Worker) Enqueue(cmd model.Command) {
	select {
	case e.cmdCh <- cmd:
	default:
		e.log.Warn("channel full, dropping command", "repo", cmd.RepoName())
	}
}

func (e *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd, ok := <-e.cmdCh:
			if !ok {
				return
			}
			switch c := cmd.(type) {
			case model.InitCommand:
				e.handleInit(ctx, c.Name)
			case model.PullCommand:
				e.handlePull(ctx, c)
			}
		}
	}
}

func (e *Worker) handleInit(ctx context.Context, name string) {
	repo, ok := e.repoConfig(name)
	if !ok {
		e.log.Error("init: unknown repo", "repo", name)
		return
	}

	bareDir := filepath.Join(e.cfg.WorkDir, name, ".bare")
	if err := e.git.BareClone(ctx, repo.URL, bareDir); err != nil {
		repoState, prev := e.transition(name, model.StateError, fmt.Errorf("%w: %w", errCloneFailed, err))
		e.notify(name, repoState, prev)
		return
	}

	repoState, prev := e.transition(name, model.StateReady, nil)
	e.notify(name, repoState, prev)
}

func (e *Worker) handlePull(ctx context.Context, cmd model.PullCommand) {
	name := cmd.Name
	log := e.log.With("repo", name, "ref", cmd.Ref)

	repo, ok := e.repoConfig(name)
	if !ok {
		log.Error("pull: unknown repo")
		return
	}

	repoState, prev := e.transition(name, model.StateSyncing, nil)
	e.notify(name, repoState, prev)

	bareDir := filepath.Join(e.cfg.WorkDir, name, ".bare")
	worktreeDir := filepath.Join(e.cfg.WorkspaceDir, name)
	tmpDir := worktreeDir + ".new"

	if err := e.git.Fetch(ctx, bareDir); err != nil {
		repoState, prev = e.transition(name, model.StateError, fmt.Errorf("%w: %w", errFetchFailed, err))
		e.notify(name, repoState, prev)
		return
	}

	if err := os.RemoveAll(tmpDir); err != nil {
		log.Warn("cleanup leftover tmpDir", "err", err)
	}

	if err := e.git.WorktreePrune(ctx, bareDir); err != nil {
		repoState, prev = e.transition(name, model.StateError, fmt.Errorf("%w: %w", errPruneFailed, err))
		e.notify(name, repoState, prev)
		return
	}

	if err := os.MkdirAll(filepath.Dir(tmpDir), 0o755); err != nil {
		repoState, prev = e.transition(name, model.StateError, fmt.Errorf("mkdir %s: %w", filepath.Dir(tmpDir), err))
		e.notify(name, repoState, prev)
		return
	}

	if err := e.git.WorktreeAdd(ctx, bareDir, tmpDir, cmd.Ref); err != nil {
		repoState, prev = e.transition(name, model.StateError, fmt.Errorf("%w: %w", errAddFailed, err))
		e.notify(name, repoState, prev)
		return
	}

	if repo.VerifyCommit {
		if err := e.git.VerifyCommit(ctx, tmpDir); err != nil {
			if cleanErr := os.RemoveAll(tmpDir); cleanErr != nil {
				log.Warn("cleanup tmpDir after verify failure", "err", cleanErr)
			}
			if pruneErr := e.git.WorktreePrune(ctx, bareDir); pruneErr != nil {
				log.Warn("prune after verify failure", "err", pruneErr)
			}
			repoState, prev = e.transition(name, model.StateError, fmt.Errorf("%w: %w", errBadSignature, err))
			e.notify(name, repoState, prev)
			return
		}
	}

	oldDir := worktreeDir + ".old"
	if err := os.RemoveAll(oldDir); err != nil {
		log.Warn("cleanup leftover oldDir", "err", err)
	}
	// os.ErrNotExist is success: no previous worktree on first pull
	if err := os.Rename(worktreeDir, oldDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		if cleanErr := os.RemoveAll(tmpDir); cleanErr != nil {
			log.Warn("cleanup tmpDir after rename-to-old failure", "err", cleanErr)
		}
		if pruneErr := e.git.WorktreePrune(ctx, bareDir); pruneErr != nil {
			log.Warn("prune after rename-to-old failure", "err", pruneErr)
		}
		repoState, prev = e.transition(name, model.StateError, fmt.Errorf("%w: %w", errSwapFailed, err))
		e.notify(name, repoState, prev)
		return
	}
	if err := os.Rename(tmpDir, worktreeDir); err != nil {
		if restoreErr := os.Rename(oldDir, worktreeDir); restoreErr != nil {
			log.Error("restore worktree after rename-to-new failure", "err", restoreErr)
		}
		if cleanErr := os.RemoveAll(tmpDir); cleanErr != nil {
			log.Warn("cleanup tmpDir after rename-to-new failure", "err", cleanErr)
		}
		if pruneErr := e.git.WorktreePrune(ctx, bareDir); pruneErr != nil {
			log.Warn("prune after rename-to-new failure", "err", pruneErr)
		}
		repoState, prev = e.transition(name, model.StateError, err)
		e.notify(name, repoState, prev)
		return
	}
	if err := os.RemoveAll(oldDir); err != nil {
		log.Warn("cleanup oldDir after successful swap", "err", err)
	}
	if err := e.git.WorktreePrune(ctx, bareDir); err != nil {
		log.Warn("prune after successful swap", "err", err)
	}

	// Pre-set Ref so the transition write captures the correct value atomically.
	rs, err := e.state.Get(name)
	if err != nil {
		log.Error("handlePull: repo missing from state store before ready transition")
		return
	}
	rs.Ref = cmd.Ref
	e.state.Set(name, rs)
	repoState, prev = e.transition(name, model.StateReady, nil)
	e.notify(name, repoState, prev)
}

func (e *Worker) transition(name string, kind model.RepoStateKind, err error) (model.RepoState, model.RepoStateKind) {
	repoState, getErr := e.state.Get(name)
	if getErr != nil {
		e.log.Error("transition: repo missing from state store", "repo", name)
		return model.RepoState{Name: name}, ""
	}
	prev := repoState.State
	repoState.State = kind
	switch kind {
	case model.StateInit:
		repoState.Error = ""
		repoState.LastUpdatedAt = nil
	case model.StateSyncing:
		repoState.Error = ""
	case model.StateReady:
		now := time.Now()
		repoState.Error = ""
		repoState.LastUpdatedAt = &now
	case model.StateError:
		if err != nil {
			repoState.Error = err.Error()
		}
	}
	e.state.Set(name, repoState)
	return repoState, prev
}

func (e *Worker) notify(name string, state model.RepoState, prev model.RepoStateKind) {
	if e.notifier == nil {
		return
	}
	e.notifier.Enqueue(model.RepoChangedEvent{
		EventKind:     model.EventKindRepoChanged,
		Name:          name,
		State:         state.State,
		PreviousState: prev,
		Ref:           state.Ref,
		LastUpdatedAt: state.LastUpdatedAt,
		Error:         state.Error,
	})
}

func (e *Worker) repoConfig(name string) (model.RepoConfig, bool) {
	for _, r := range e.cfg.Repos {
		if r.Name == name {
			return r, true
		}
	}
	return model.RepoConfig{}, false
}
