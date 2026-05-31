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

const defaultEnqueueTimeout = 3 * time.Second

var (
	errCloneFailed  = errors.New("clone failed")
	errFetchFailed  = errors.New("fetch error")
	errPruneFailed  = errors.New("prune error")
	errAddFailed    = errors.New("add error")
	errSwapFailed   = errors.New("swap failed")
	errBadSignature = errors.New("bad signature")
)

type Worker struct {
	cfg            *Config
	repo           model.RepoConfig
	state          *storage.StateStore
	git            client.GitClient
	notifyCh       chan<- model.RepoChangedEvent
	log            *slog.Logger
	cmdCh          chan model.Command
	enqueueTimeout time.Duration
}

func NewWorker(cfg *Config, repo model.RepoConfig, cmdCh chan model.Command, state *storage.StateStore, git client.GitClient, notifyCh chan<- model.RepoChangedEvent, log *slog.Logger) *Worker {
	return &Worker{
		cfg:            cfg,
		repo:           repo,
		state:          state,
		git:            git,
		notifyCh:       notifyCh,
		log:            log,
		cmdCh:          cmdCh,
		enqueueTimeout: defaultEnqueueTimeout,
	}
}

func (w *Worker) Enqueue(cmd model.Command) error {
	t := time.NewTimer(w.enqueueTimeout)
	defer t.Stop()
	select {
	case w.cmdCh <- cmd:
		return nil
	case <-t.C:
		w.log.Warn("enqueue timeout: worker queue full", "repo", cmd.RepoName())
		return fmt.Errorf("worker queue full for repo %s", cmd.RepoName())
	}
}

func (w *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd, ok := <-w.cmdCh:
			if !ok {
				return
			}
			switch c := cmd.(type) {
			case model.InitCommand:
				rs, err := w.state.Get(c.Name)
				if err == nil && rs.State == model.StateResetting {
					w.log.Info("skip init: reset in progress", "repo", c.Name)
					continue
				}
				w.handleInit(ctx)
			case model.PullCommand:
				rs, err := w.state.Get(c.Name)
				if err == nil && rs.State == model.StateResetting {
					w.log.Info("skip pull: reset in progress", "repo", c.Name, "ref", c.Ref)
					continue
				}
				w.handlePull(ctx, c)
			case model.ResetCommand:
				w.handleReset(ctx, c)
			}
		}
	}
}

func (w *Worker) handleInit(ctx context.Context) {
	name := w.repo.Name
	bareDir := filepath.Join(w.cfg.WorkDir, name, ".bare")

	_, err := os.Stat(filepath.Join(bareDir, "HEAD"))
	if err == nil {
		repoState, prev := w.transition(name, model.StateReady, "", nil)
		w.notify(name, repoState, prev)
		return
	}

	err = w.git.BareClone(ctx, w.repo.URL, bareDir)
	if err != nil {
		repoState, prev := w.transition(name, model.StateError, "", fmt.Errorf("%w: %w", errCloneFailed, err))
		w.notify(name, repoState, prev)
		return
	}

	repoState, prev := w.transition(name, model.StateReady, "", nil)
	w.notify(name, repoState, prev)
}

func (w *Worker) handlePull(ctx context.Context, cmd model.PullCommand) {
	name := w.repo.Name
	log := w.log.With("repo", name, "ref", cmd.Ref)

	repoState, prev := w.transition(name, model.StateSyncing, "", nil)
	w.notify(name, repoState, prev)

	bareDir := filepath.Join(w.cfg.WorkDir, name, ".bare")
	worktreeDir := filepath.Join(w.cfg.WorkspaceDir, name)
	tmpDir := worktreeDir + ".new"

	err := w.git.Fetch(ctx, bareDir)
	if err != nil {
		repoState, prev = w.transition(name, model.StateError, "", fmt.Errorf("%w: %w", errFetchFailed, err))
		w.notify(name, repoState, prev)
		return
	}

	err = os.RemoveAll(tmpDir)
	if err != nil {
		log.Warn("cleanup leftover tmpDir", "err", err)
	}

	err = w.git.WorktreePrune(ctx, bareDir)
	if err != nil {
		repoState, prev = w.transition(name, model.StateError, "", fmt.Errorf("%w: %w", errPruneFailed, err))
		w.notify(name, repoState, prev)
		return
	}

	err = os.MkdirAll(filepath.Dir(tmpDir), 0o755)
	if err != nil {
		repoState, prev = w.transition(name, model.StateError, "", fmt.Errorf("mkdir %s: %w", filepath.Dir(tmpDir), err))
		w.notify(name, repoState, prev)
		return
	}

	err = w.git.WorktreeAdd(ctx, bareDir, tmpDir, cmd.Ref)
	if err != nil {
		repoState, prev = w.transition(name, model.StateError, "", fmt.Errorf("%w: %w", errAddFailed, err))
		w.notify(name, repoState, prev)
		return
	}

	if w.repo.VerifyCommit {
		if err := w.git.VerifyCommit(ctx, tmpDir); err != nil {
			if cleanErr := os.RemoveAll(tmpDir); cleanErr != nil {
				log.Warn("cleanup tmpDir after verify failure", "err", cleanErr)
			}
			if pruneErr := w.git.WorktreePrune(ctx, bareDir); pruneErr != nil {
				log.Warn("prune after verify failure", "err", pruneErr)
			}
			repoState, prev = w.transition(name, model.StateError, "", fmt.Errorf("%w: %w", errBadSignature, err))
			w.notify(name, repoState, prev)
			return
		}
	}

	oldDir := worktreeDir + ".old"
	if err := os.RemoveAll(oldDir); err != nil {
		log.Warn("cleanup leftover oldDir", "err", err)
	}
	// os.ErrNotExist is success: no previous worktree on first pull
	err = os.Rename(worktreeDir, oldDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		if cleanErr := os.RemoveAll(tmpDir); cleanErr != nil {
			log.Warn("cleanup tmpDir after rename-to-old failure", "err", cleanErr)
		}
		if pruneErr := w.git.WorktreePrune(ctx, bareDir); pruneErr != nil {
			log.Warn("prune after rename-to-old failure", "err", pruneErr)
		}
		repoState, prev = w.transition(name, model.StateError, "", fmt.Errorf("%w: %w", errSwapFailed, err))
		w.notify(name, repoState, prev)
		return
	}
	err = os.Rename(tmpDir, worktreeDir)
	if err != nil {
		if restoreErr := os.Rename(oldDir, worktreeDir); restoreErr != nil {
			log.Error("restore worktree after rename-to-new failure", "err", restoreErr)
		}
		if cleanErr := os.RemoveAll(tmpDir); cleanErr != nil {
			log.Warn("cleanup tmpDir after rename-to-new failure", "err", cleanErr)
		}
		if pruneErr := w.git.WorktreePrune(ctx, bareDir); pruneErr != nil {
			log.Warn("prune after rename-to-new failure", "err", pruneErr)
		}
		repoState, prev = w.transition(name, model.StateError, "", err)
		w.notify(name, repoState, prev)
		return
	}
	err = os.RemoveAll(oldDir)
	if err != nil {
		log.Warn("cleanup oldDir after successful swap", "err", err)
	}
	err = w.git.WorktreePrune(ctx, bareDir)
	if err != nil {
		log.Warn("prune after successful swap", "err", err)
	}

	repoState, prev = w.transition(name, model.StateReady, cmd.Ref, nil)
	w.notify(name, repoState, prev)
}

func (w *Worker) handleReset(_ context.Context, cmd model.ResetCommand) {
	name := w.repo.Name
	err := os.RemoveAll(filepath.Join(w.cfg.WorkDir, name))
	if err != nil {
		repoState, prev := w.transition(name, model.StateError, "", fmt.Errorf("remove work dir: %w", err))
		w.notify(name, repoState, prev)
		return
	}
	err = os.RemoveAll(filepath.Join(w.cfg.WorkspaceDir, name))
	if err != nil {
		repoState, prev := w.transition(name, model.StateError, "", fmt.Errorf("remove workspace dir: %w", err))
		w.notify(name, repoState, prev)
		return
	}
	repoState, prev := w.transition(name, model.StateInit, "", nil)
	w.notify(name, repoState, prev)
}

// transition updates repo state atomically. ref is only applied for StateReady transitions.
func (w *Worker) transition(name string, kind model.RepoStateKind, ref string, err error) (model.RepoState, model.RepoStateKind) {
	repoState, getErr := w.state.Get(name)
	if getErr != nil {
		w.log.Error("transition: repo missing from state store", "repo", name)
		return model.RepoState{Name: name}, ""
	}
	prev := repoState.State
	repoState.State = kind
	switch kind {
	case model.StateInit:
		repoState.Ref = ""
		repoState.Error = ""
		repoState.LastUpdatedAt = nil
	case model.StateSyncing:
		repoState.Error = ""
	case model.StateReady:
		now := time.Now()
		repoState.Error = ""
		repoState.LastUpdatedAt = &now
		if ref != "" {
			repoState.Ref = ref
		}
	case model.StateError:
		if err != nil {
			repoState.Error = err.Error()
		}
	}
	w.state.Set(name, repoState)
	return repoState, prev
}

func (w *Worker) notify(name string, state model.RepoState, prev model.RepoStateKind) {
	if w.notifyCh == nil {
		return
	}
	event := model.RepoChangedEvent{
		EventKind:     model.EventKindRepoChanged,
		Name:          name,
		State:         state.State,
		PreviousState: prev,
		Ref:           state.Ref,
		LastUpdatedAt: state.LastUpdatedAt,
		Error:         state.Error,
	}
	t := time.NewTimer(w.enqueueTimeout)
	defer t.Stop()
	select {
	case w.notifyCh <- event:
	case <-t.C:
		w.log.Warn("notify: notification queue full, dropping event", "repo", name, "state", state.State)
	}
}
