package server

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
	"github.com/junpeng-jp/gitops-sidecar/internal/storage"
	"github.com/junpeng-jp/gitops-sidecar/internal/testutils/mocks"
)

func newTestWorker(t *testing.T, repo model.RepoConfig) (*Worker, *storage.StateStore, *mocks.MockGitClient) {
	t.Helper()
	cfg := &Config{
		WorkDir:      t.TempDir(),
		WorkspaceDir: t.TempDir(),
		Repos:        []model.RepoConfig{repo},
	}
	state := &storage.StateStore{}
	state.Init([]model.RepoConfig{repo}, cfg.WorkspaceDir)
	git := &mocks.MockGitClient{}
	cmdCh := make(chan model.Command, 16)
	engine := NewWorker(cfg, repo, cmdCh, state, git, nil, slog.Default())
	engine.enqueueTimeout = 50 * time.Millisecond

	return engine, state, git
}

func TestWorker_HandleInit(t *testing.T) {
	t.Parallel()
	repo := model.RepoConfig{Name: "r", URL: "git@github.com/r.git"}

	testCases := []struct {
		name          string
		setupMock     func(git *mocks.MockGitClient, bareDir string)
		expectedState model.RepoStateKind
		expectedErr   error
	}{
		{
			name: "happy path: bare clone succeeds transitions to ready",
			setupMock: func(git *mocks.MockGitClient, bareDir string) {
				git.On("BareClone", mock.Anything, "git@github.com/r.git", bareDir).Return(nil)
			},
			expectedState: model.StateReady,
		},
		{
			name: "happy path: existing bare repo skips clone",
			setupMock: func(git *mocks.MockGitClient, bareDir string) {
				_ = os.MkdirAll(bareDir, 0o750)
				_ = os.WriteFile(filepath.Join(bareDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600)
			},
			expectedState: model.StateReady,
		},
		{
			name: "error path: bare clone fails",
			setupMock: func(git *mocks.MockGitClient, bareDir string) {
				git.On("BareClone", mock.Anything, "git@github.com/r.git", bareDir).Return(errCloneFailed)
			},
			expectedState: model.StateError,
			expectedErr:   errCloneFailed,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine, state, git := newTestWorker(t, repo)

			bareDir := filepath.Join(engine.cfg.WorkDir, "r", ".bare")
			tc.setupMock(git, bareDir)

			engine.handleInit(context.Background())

			rs, err := state.Get("r")
			require.NoError(t, err)
			assert.Equal(t, tc.expectedState, rs.State)
			if tc.expectedErr != nil {
				assert.Contains(t, rs.Error, tc.expectedErr.Error())
			} else {
				assert.Empty(t, rs.Error)
			}

			select {
			case <-engine.cmdCh:
				t.Fatal("unexpected command enqueued")
			default:
			}

			git.AssertExpectations(t)
		})
	}
}

func TestWorker_HandlePull(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name          string
		verifyCommit  bool
		setupMock     func(git *mocks.MockGitClient, bareDir, worktreeDir string)
		expectedState model.RepoStateKind
		expectedErr   error
		expectUpdated bool
		expectedRef   string
	}{
		{
			name: "happy path: sync succeeds",
			setupMock: func(git *mocks.MockGitClient, bareDir, wtDir string) {
				tmpDir := wtDir + ".new"
				git.On("Fetch", mock.Anything, bareDir).Return(nil)
				git.On("WorktreePrune", mock.Anything, bareDir).Return(nil)
				git.On("WorktreeAdd", mock.Anything, bareDir, tmpDir, "main").
					Return(nil).
					Run(func(args mock.Arguments) { _ = os.MkdirAll(args.String(2), 0o750) })
			},
			expectedState: model.StateReady,
			expectUpdated: true,
			expectedRef:   "main",
		},
		{
			name:         "happy path: sync succeeds with commit verification",
			verifyCommit: true,
			setupMock: func(git *mocks.MockGitClient, bareDir, wtDir string) {
				tmpDir := wtDir + ".new"
				git.On("Fetch", mock.Anything, bareDir).Return(nil)
				git.On("WorktreePrune", mock.Anything, bareDir).Return(nil)
				git.On("WorktreeAdd", mock.Anything, bareDir, tmpDir, "main").
					Return(nil).
					Run(func(args mock.Arguments) { _ = os.MkdirAll(args.String(2), 0o750) })
				git.On("VerifyCommit", mock.Anything, tmpDir).Return(nil)
			},
			expectedState: model.StateReady,
			expectUpdated: true,
			expectedRef:   "main",
		},
		{
			name: "error path: fetch fails",
			setupMock: func(git *mocks.MockGitClient, bareDir, _ string) {
				git.On("Fetch", mock.Anything, bareDir).Return(errFetchFailed)
			},
			expectedState: model.StateError,
			expectedErr:   errFetchFailed,
		},
		{
			name: "error path: worktree prune fails",
			setupMock: func(git *mocks.MockGitClient, bareDir, _ string) {
				git.On("Fetch", mock.Anything, bareDir).Return(nil)
				git.On("WorktreePrune", mock.Anything, bareDir).Return(errPruneFailed)
			},
			expectedState: model.StateError,
			expectedErr:   errPruneFailed,
		},
		{
			name: "error path: worktree add fails",
			setupMock: func(git *mocks.MockGitClient, bareDir, wtDir string) {
				tmpDir := wtDir + ".new"
				git.On("Fetch", mock.Anything, bareDir).Return(nil)
				git.On("WorktreePrune", mock.Anything, bareDir).Return(nil)
				git.On("WorktreeAdd", mock.Anything, bareDir, tmpDir, "main").Return(errAddFailed)
			},
			expectedState: model.StateError,
			expectedErr:   errAddFailed,
		},
		{
			name:         "error path: commit verification fails",
			verifyCommit: true,
			setupMock: func(git *mocks.MockGitClient, bareDir, wtDir string) {
				tmpDir := wtDir + ".new"
				git.On("Fetch", mock.Anything, bareDir).Return(nil)
				git.On("WorktreePrune", mock.Anything, bareDir).Return(nil)
				git.On("WorktreeAdd", mock.Anything, bareDir, tmpDir, "main").Return(nil)
				git.On("VerifyCommit", mock.Anything, tmpDir).Return(errBadSignature)
			},
			expectedState: model.StateError,
			expectedErr:   errBadSignature,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := model.RepoConfig{Name: "r", URL: "url", VerifyCommit: tc.verifyCommit}
			engine, state, git := newTestWorker(t, repo)

			bareDir := filepath.Join(engine.cfg.WorkDir, "r", ".bare")
			worktreeDir := filepath.Join(engine.cfg.WorkspaceDir, "r")
			tc.setupMock(git, bareDir, worktreeDir)

			engine.handlePull(context.Background(), model.PullCommand{Name: "r", Ref: "main"})

			rs, err := state.Get("r")
			require.NoError(t, err)
			assert.Equal(t, tc.expectedState, rs.State)
			if tc.expectedErr != nil {
				assert.Contains(t, rs.Error, tc.expectedErr.Error())
			} else {
				assert.Empty(t, rs.Error)
			}
			if tc.expectUpdated {
				assert.NotNil(t, rs.LastUpdatedAt)
			}
			if tc.expectedRef != "" {
				assert.Equal(t, tc.expectedRef, rs.Ref)
			}

			git.AssertExpectations(t)
		})
	}
}

func TestWorker_HandleReset(t *testing.T) {
	t.Parallel()
	repo := model.RepoConfig{Name: "r", URL: "url"}

	t.Run("happy path: removes repo dirs and transitions to init", func(t *testing.T) {
		t.Parallel()
		engine, state, _ := newTestWorker(t, repo)

		bareDir := filepath.Join(engine.cfg.WorkDir, "r", ".bare")
		require.NoError(t, os.MkdirAll(bareDir, 0o750))
		sentinel := filepath.Join(bareDir, "HEAD")
		require.NoError(t, os.WriteFile(sentinel, []byte("ref: refs/heads/main\n"), 0o600))

		engine.handleReset(context.Background(), model.ResetCommand{Name: "r"})

		assert.NoDirExists(t, filepath.Join(engine.cfg.WorkDir, "r"))
		rs, err := state.Get("r")
		require.NoError(t, err)
		assert.Equal(t, model.StateInit, rs.State)
		assert.Empty(t, rs.Ref)
		assert.Empty(t, rs.Error)
		assert.Nil(t, rs.LastUpdatedAt)
	})

	t.Run("happy path: succeeds when dirs do not exist", func(t *testing.T) {
		t.Parallel()
		engine, state, _ := newTestWorker(t, repo)

		engine.handleReset(context.Background(), model.ResetCommand{Name: "r"})

		rs, err := state.Get("r")
		require.NoError(t, err)
		assert.Equal(t, model.StateInit, rs.State)
	})
}

func TestWorker_Enqueue(t *testing.T) {
	t.Parallel()
	repo := model.RepoConfig{Name: "r", URL: "url"}

	t.Run("happy path: enqueues when channel has capacity", func(t *testing.T) {
		t.Parallel()
		engine, _, _ := newTestWorker(t, repo)
		err := engine.Enqueue(model.PullCommand{Name: "r"})
		require.NoError(t, err)
		assert.Len(t, engine.cmdCh, 1)
	})

	t.Run("error path: returns error when channel is full", func(t *testing.T) {
		t.Parallel()
		engine, _, _ := newTestWorker(t, repo)

		for range cap(engine.cmdCh) {
			require.NoError(t, engine.Enqueue(model.PullCommand{Name: "r"}))
		}

		start := time.Now()
		err := engine.Enqueue(model.PullCommand{Name: "r"})
		elapsed := time.Since(start)

		require.Error(t, err)
		assert.GreaterOrEqual(t, elapsed, 40*time.Millisecond)
		assert.Less(t, elapsed, 500*time.Millisecond)
	})
}

func TestWorker_Run(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name          string
		repo          model.RepoConfig
		setupMock     func(git *mocks.MockGitClient, bareDir, worktreeDir string)
		enqueue       func(t *testing.T, engine *Worker)
		expectedState model.RepoStateKind
	}{
		{
			name: "happy path: init then pull reaches ready state",
			repo: model.RepoConfig{Name: "r", URL: "git@github.com/r.git"},
			setupMock: func(git *mocks.MockGitClient, bareDir, wtDir string) {
				tmpDir := wtDir + ".new"
				git.On("BareClone", mock.Anything, "git@github.com/r.git", bareDir).Return(nil)
				git.On("Fetch", mock.Anything, bareDir).Return(nil)
				git.On("WorktreePrune", mock.Anything, bareDir).Return(nil)
				git.On("WorktreeAdd", mock.Anything, bareDir, tmpDir, "main").
					Return(nil).
					Run(func(args mock.Arguments) { _ = os.MkdirAll(args.String(2), 0o750) })
			},
			enqueue: func(t *testing.T, e *Worker) {
				t.Helper()
				require.NoError(t, e.Enqueue(model.InitCommand{Name: "r"}))
				require.NoError(t, e.Enqueue(model.PullCommand{Name: "r", Ref: "main"}))
			},
			expectedState: model.StateReady,
		},
		{
			name: "error path: bare clone failure leaves repo in error state",
			repo: model.RepoConfig{Name: "r", URL: "url"},
			setupMock: func(git *mocks.MockGitClient, bareDir, _ string) {
				git.On("BareClone", mock.Anything, "url", bareDir).Return(errCloneFailed)
			},
			enqueue: func(t *testing.T, e *Worker) {
				t.Helper()
				require.NoError(t, e.Enqueue(model.InitCommand{Name: "r"}))
			},
			expectedState: model.StateError,
		},
		{
			name: "edge case: nil notify client does not panic",
			repo: model.RepoConfig{Name: "r", URL: "url"},
			setupMock: func(git *mocks.MockGitClient, bareDir, _ string) {
				git.On("BareClone", mock.Anything, "url", bareDir).Return(errCloneFailed)
			},
			enqueue: func(t *testing.T, e *Worker) {
				t.Helper()
				require.NoError(t, e.Enqueue(model.InitCommand{Name: "r"}))
			},
			expectedState: model.StateError,
		},
		{
			name:      "edge case: pull skipped when repo is resetting",
			repo:      model.RepoConfig{Name: "r", URL: "url"},
			setupMock: func(_ *mocks.MockGitClient, _, _ string) {},
			enqueue: func(t *testing.T, e *Worker) {
				t.Helper()
				rs, err := e.state.Get("r")
				require.NoError(t, err)
				rs.State = model.StateResetting
				e.state.Set("r", rs)
				require.NoError(t, e.Enqueue(model.PullCommand{Name: "r", Ref: "main"}))
				// Follow with reset so the worker has something to drain to after the skip.
				require.NoError(t, e.Enqueue(model.ResetCommand{Name: "r"}))
			},
			expectedState: model.StateInit,
		},
		{
			name:      "edge case: init skipped when repo is resetting",
			repo:      model.RepoConfig{Name: "r", URL: "url"},
			setupMock: func(_ *mocks.MockGitClient, _, _ string) {},
			enqueue: func(t *testing.T, e *Worker) {
				t.Helper()
				rs, err := e.state.Get("r")
				require.NoError(t, err)
				rs.State = model.StateResetting
				e.state.Set("r", rs)
				require.NoError(t, e.Enqueue(model.InitCommand{Name: "r"}))
				// Follow with reset so the worker has something to drain to after the skip.
				require.NoError(t, e.Enqueue(model.ResetCommand{Name: "r"}))
			},
			expectedState: model.StateInit,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine, state, git := newTestWorker(t, tc.repo)

			bareDir := filepath.Join(engine.cfg.WorkDir, "r", ".bare")
			worktreeDir := filepath.Join(engine.cfg.WorkspaceDir, "r")
			tc.setupMock(git, bareDir, worktreeDir)

			ctx := t.Context()
			go engine.Run(ctx)

			tc.enqueue(t, engine)

			require.Eventually(t, func() bool {
				rs, err := state.Get("r")
				require.NoError(t, err)

				return rs.State == tc.expectedState
			}, 5*time.Second, 10*time.Millisecond)

			git.AssertExpectations(t)
		})
	}
}
