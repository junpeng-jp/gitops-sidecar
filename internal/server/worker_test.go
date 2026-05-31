package server

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
	"github.com/junpeng-jp/gitops-sidecar/internal/storage"
	"github.com/junpeng-jp/gitops-sidecar/internal/testutils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestWorker(t *testing.T, repos []model.RepoConfig) (*Worker, *storage.StateStore, *mocks.MockGitClient) {
	t.Helper()
	cfg := &Config{
		WorkDir:      t.TempDir(),
		WorkspaceDir: t.TempDir(),
		Repos:        repos,
	}
	state := &storage.StateStore{}
	state.Init(repos, cfg.WorkspaceDir)
	git := &mocks.MockGitClient{}
	engine := NewWorker(cfg, state, git, nil, slog.Default())
	return engine, state, git
}

func TestWorker_HandleInit(t *testing.T) {
	t.Parallel()
	repos := []model.RepoConfig{{Name: "r", URL: "git@github.com/r.git"}}

	testCases := []struct {
		name          string
		targetName    string
		setupMock     func(git *mocks.MockGitClient, bareDir string)
		checkRepoName string
		expectedState model.RepoStateKind
		expectedErr   error
	}{
		{
			name:       "happy path: bare clone succeeds transitions to ready",
			targetName: "r",
			setupMock: func(git *mocks.MockGitClient, bareDir string) {
				git.On("BareClone", mock.Anything, "git@github.com/r.git", bareDir).Return(nil)
			},
			checkRepoName: "r",
			expectedState: model.StateReady,
		},
		{
			name:       "error path: bare clone fails",
			targetName: "r",
			setupMock: func(git *mocks.MockGitClient, bareDir string) {
				git.On("BareClone", mock.Anything, "git@github.com/r.git", bareDir).Return(errCloneFailed)
			},
			checkRepoName: "r",
			expectedState: model.StateError,
			expectedErr:   errCloneFailed,
		},
		{
			name:          "error path: unknown repo is ignored",
			targetName:    "nonexistent",
			setupMock:     func(*mocks.MockGitClient, string) {},
			checkRepoName: "r",
			expectedState: model.StateInit,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine, state, git := newTestWorker(t, repos)

			bareDir := filepath.Join(engine.cfg.WorkDir, "r", ".bare")
			tc.setupMock(git, bareDir)

			engine.handleInit(context.Background(), tc.targetName)

			rs, ok := state.Get(tc.checkRepoName)
			require.True(t, ok)
			assert.Equal(t, tc.expectedState, rs.State)
			if tc.expectedErr != nil {
				assert.Equal(t, tc.expectedErr.Error(), rs.Error)
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
		targetName    string
		setupMock     func(git *mocks.MockGitClient, bareDir, worktreeDir string)
		checkRepoName string
		expectedState model.RepoStateKind
		expectedErr   error
		expectUpdated bool
	}{
		{
			name:       "happy path: sync succeeds",
			targetName: "r",
			setupMock: func(git *mocks.MockGitClient, bareDir, wtDir string) {
				tmpDir := wtDir + ".new"
				git.On("Fetch", mock.Anything, bareDir).Return(nil)
				git.On("WorktreePrune", mock.Anything, bareDir).Return(nil)
				git.On("WorktreeAdd", mock.Anything, bareDir, tmpDir, "main").
					Return(nil).
					Run(func(args mock.Arguments) { os.MkdirAll(args.String(2), 0o755) })
			},
			checkRepoName: "r",
			expectedState: model.StateReady,
			expectUpdated: true,
		},
		{
			name:         "happy path: sync succeeds with commit verification",
			verifyCommit: true,
			targetName:   "r",
			setupMock: func(git *mocks.MockGitClient, bareDir, wtDir string) {
				tmpDir := wtDir + ".new"
				git.On("Fetch", mock.Anything, bareDir).Return(nil)
				git.On("WorktreePrune", mock.Anything, bareDir).Return(nil)
				git.On("WorktreeAdd", mock.Anything, bareDir, tmpDir, "main").
					Return(nil).
					Run(func(args mock.Arguments) { os.MkdirAll(args.String(2), 0o755) })
				git.On("VerifyCommit", mock.Anything, tmpDir).Return(nil)
			},
			checkRepoName: "r",
			expectedState: model.StateReady,
			expectUpdated: true,
		},
		{
			name:       "error path: fetch fails",
			targetName: "r",
			setupMock: func(git *mocks.MockGitClient, bareDir, _ string) {
				git.On("Fetch", mock.Anything, bareDir).Return(errFetchFailed)
			},
			checkRepoName: "r",
			expectedState: model.StateError,
			expectedErr:   errFetchFailed,
		},
		{
			name:       "error path: worktree prune fails",
			targetName: "r",
			setupMock: func(git *mocks.MockGitClient, bareDir, _ string) {
				git.On("Fetch", mock.Anything, bareDir).Return(nil)
				git.On("WorktreePrune", mock.Anything, bareDir).Return(errPruneFailed)
			},
			checkRepoName: "r",
			expectedState: model.StateError,
			expectedErr:   errPruneFailed,
		},
		{
			name:       "error path: worktree add fails",
			targetName: "r",
			setupMock: func(git *mocks.MockGitClient, bareDir, wtDir string) {
				tmpDir := wtDir + ".new"
				git.On("Fetch", mock.Anything, bareDir).Return(nil)
				git.On("WorktreePrune", mock.Anything, bareDir).Return(nil)
				git.On("WorktreeAdd", mock.Anything, bareDir, tmpDir, "main").Return(errAddFailed)
			},
			checkRepoName: "r",
			expectedState: model.StateError,
			expectedErr:   errAddFailed,
		},
		{
			name:         "error path: commit verification fails",
			verifyCommit: true,
			targetName:   "r",
			setupMock: func(git *mocks.MockGitClient, bareDir, wtDir string) {
				tmpDir := wtDir + ".new"
				git.On("Fetch", mock.Anything, bareDir).Return(nil)
				git.On("WorktreePrune", mock.Anything, bareDir).Return(nil) // called twice: before add and cleanup
				git.On("WorktreeAdd", mock.Anything, bareDir, tmpDir, "main").Return(nil)
				git.On("VerifyCommit", mock.Anything, tmpDir).Return(errBadSignature)
			},
			checkRepoName: "r",
			expectedState: model.StateError,
			expectedErr:   errBadSignature,
		},
		{
			name:          "error path: unknown repo is ignored",
			targetName:    "nonexistent",
			setupMock:     func(*mocks.MockGitClient, string, string) {},
			checkRepoName: "r",
			expectedState: model.StateInit,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repos := []model.RepoConfig{{Name: "r", URL: "url", VerifyCommit: tc.verifyCommit}}
			engine, state, git := newTestWorker(t, repos)

			bareDir := filepath.Join(engine.cfg.WorkDir, "r", ".bare")
			worktreeDir := filepath.Join(engine.cfg.WorkspaceDir, "r")
			tc.setupMock(git, bareDir, worktreeDir)

			engine.handlePull(context.Background(), model.PullCommand{Name: tc.targetName, Ref: "main"})

			rs, ok := state.Get(tc.checkRepoName)
			require.True(t, ok)
			assert.Equal(t, tc.expectedState, rs.State)
			if tc.expectedErr != nil {
				assert.Equal(t, tc.expectedErr.Error(), rs.Error)
			} else {
				assert.Empty(t, rs.Error)
			}
			if tc.expectUpdated {
				assert.NotNil(t, rs.LastUpdatedAt)
			}

			git.AssertExpectations(t)
		})
	}
}

func TestWorker_Enqueue(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name       string
		prefill    int
		expectDrop bool
	}{
		{
			name:       "happy path: enqueues when channel has capacity",
			prefill:    0,
			expectDrop: false,
		},
		{
			name:       "edge case: silently drops when channel is full",
			prefill:    16,
			expectDrop: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repos := []model.RepoConfig{{Name: "r", URL: "url"}}
			engine, _, _ := newTestWorker(t, repos)

			for i := 0; i < tc.prefill; i++ {
				engine.Enqueue(model.PullCommand{Name: "r"})
			}

			done := make(chan struct{})
			go func() {
				engine.Enqueue(model.PullCommand{Name: "r"})
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(100 * time.Millisecond):
				t.Fatal("Enqueue blocked unexpectedly")
			}

			if !tc.expectDrop {
				assert.Len(t, engine.cmdCh, 1)
			} else {
				assert.Len(t, engine.cmdCh, cap(engine.cmdCh))
			}
		})
	}
}

func TestWorker_Run(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name          string
		repos         []model.RepoConfig
		nilNotify     bool
		setupMock     func(git *mocks.MockGitClient, bareDir, worktreeDir string)
		enqueue       func(engine *Worker)
		expectedState model.RepoStateKind
	}{
		{
			name:  "happy path: init then pull reaches ready state",
			repos: []model.RepoConfig{{Name: "r", URL: "git@github.com/r.git"}},
			setupMock: func(git *mocks.MockGitClient, bareDir, wtDir string) {
				tmpDir := wtDir + ".new"
				git.On("BareClone", mock.Anything, "git@github.com/r.git", bareDir).Return(nil)
				git.On("Fetch", mock.Anything, bareDir).Return(nil)
				git.On("WorktreePrune", mock.Anything, bareDir).Return(nil)
				git.On("WorktreeAdd", mock.Anything, bareDir, tmpDir, "main").
					Return(nil).
					Run(func(args mock.Arguments) { os.MkdirAll(args.String(2), 0o755) })
			},
			enqueue: func(e *Worker) {
				e.Enqueue(model.InitCommand{Name: "r"})
				e.Enqueue(model.PullCommand{Name: "r", Ref: "main"})
			},
			expectedState: model.StateReady,
		},
		{
			name:  "error path: bare clone failure leaves repo in error state",
			repos: []model.RepoConfig{{Name: "r", URL: "url"}},
			setupMock: func(git *mocks.MockGitClient, bareDir, _ string) {
				git.On("BareClone", mock.Anything, "url", bareDir).Return(errCloneFailed)
			},
			enqueue: func(e *Worker) {
				e.Enqueue(model.InitCommand{Name: "r"})
			},
			expectedState: model.StateError,
		},
		{
			name:      "edge case: nil notify client does not panic",
			repos:     []model.RepoConfig{{Name: "r", URL: "url"}},
			nilNotify: true,
			setupMock: func(git *mocks.MockGitClient, bareDir, _ string) {
				git.On("BareClone", mock.Anything, "url", bareDir).Return(errCloneFailed)
			},
			enqueue: func(e *Worker) {
				e.Enqueue(model.InitCommand{Name: "r"})
			},
			expectedState: model.StateError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine, state, git := newTestWorker(t, tc.repos)

			bareDir := filepath.Join(engine.cfg.WorkDir, "r", ".bare")
			worktreeDir := filepath.Join(engine.cfg.WorkspaceDir, "r")
			tc.setupMock(git, bareDir, worktreeDir)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go engine.Run(ctx)

			tc.enqueue(engine)

			require.Eventually(t, func() bool {
				rs, _ := state.Get("r")
				return rs.State == tc.expectedState
			}, 5*time.Second, 10*time.Millisecond)

			git.AssertExpectations(t)
		})
	}
}
