package server

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
	"github.com/junpeng-jp/gitops-sidecar/internal/storage"
	"github.com/junpeng-jp/gitops-sidecar/internal/testutils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestGitOpsController(t *testing.T, repos []model.RepoConfig) (*GitOpsController, *Config, *storage.StateStore, map[string]*Worker, map[string]*mocks.MockGitClient) {
	t.Helper()
	cfg := &Config{
		WorkDir:      t.TempDir(),
		WorkspaceDir: t.TempDir(),
		Repos:        repos,
	}
	state := &storage.StateStore{}
	state.Init(repos, cfg.WorkspaceDir)
	workers := make(map[string]*Worker, len(repos))
	gits := make(map[string]*mocks.MockGitClient, len(repos))
	for _, repo := range repos {
		git := &mocks.MockGitClient{}
		cmdCh := make(chan model.Command, 16)
		w := NewWorker(cfg, repo, cmdCh, state, git, nil, slog.Default())
		w.enqueueTimeout = 50 * time.Millisecond
		workers[repo.Name] = w
		gits[repo.Name] = git
	}
	c := &GitOpsController{cfg: cfg, state: state, workers: workers, log: slog.Default()}
	return c, cfg, state, workers, gits
}

func TestGetRepos(t *testing.T) {
	t.Parallel()
	repos := []model.RepoConfig{{Name: "r", URL: "url"}}

	testCases := []struct {
		name             string
		ctx              context.Context
		request          model.GetReposRequest
		expectedErr      error
		expectedResponse model.GetReposResponse
	}{
		{
			name:             "happy path: returns repo list",
			ctx:              context.Background(),
			request:          model.GetReposRequest{Limit: 10},
			expectedResponse: model.GetReposResponse{Repos: []model.RepoState{{Name: "r", State: model.StateInit}}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, _, _, _, _ := newTestGitOpsController(t, repos)
			resp, err := c.GetRepos(tc.ctx, tc.request)
			if tc.expectedErr != nil {
				require.ErrorIs(t, err, tc.expectedErr)
				return
			}
			require.NoError(t, err)
			for i := range resp.Repos {
				resp.Repos[i].Path = ""
			}
			assert.Equal(t, tc.expectedResponse, resp)
		})
	}
}

func TestGetRepo(t *testing.T) {
	t.Parallel()
	repos := []model.RepoConfig{{Name: "r", URL: "url"}}

	testCases := []struct {
		name             string
		ctx              context.Context
		request          model.GetRepoRequest
		expectedErr      error
		expectedResponse model.GetRepoResponse
	}{
		{
			name:             "happy path: returns repo state",
			ctx:              context.Background(),
			request:          model.GetRepoRequest{Name: "r"},
			expectedResponse: model.GetRepoResponse{Repo: model.RepoState{Name: "r", State: model.StateInit}},
		},
		{
			name:        "error path: not found for unknown repo",
			ctx:         context.Background(),
			request:     model.GetRepoRequest{Name: "unknown"},
			expectedErr: errNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, _, _, _, _ := newTestGitOpsController(t, repos)
			resp, err := c.GetRepo(tc.ctx, tc.request)
			if tc.expectedErr != nil {
				require.ErrorIs(t, err, tc.expectedErr)
				return
			}
			require.NoError(t, err)
			resp.Repo.Path = ""
			assert.Equal(t, tc.expectedResponse, resp)
		})
	}
}

func TestRepoOperation(t *testing.T) {
	t.Parallel()
	defaultRepoConfig := []model.RepoConfig{{Name: "r", URL: "url"}}

	readyWithBare := func(t *testing.T, cfg *Config, s *storage.StateStore) {
		t.Helper()
		rs, err := s.Get("r")
		require.NoError(t, err)
		rs.State = model.StateReady
		s.Set("r", rs)
		require.NoError(t, os.MkdirAll(filepath.Join(cfg.WorkDir, "r", ".bare"), 0o755))
	}

	testCases := []struct {
		name             string
		ctx              context.Context
		setup            func(*testing.T, *Config, *storage.StateStore)
		repoConfig       []model.RepoConfig
		request          model.RepoOperationRequest
		expectedErr      error
		expectedResponse model.RepoOperationResponse
	}{
		{
			name:             "happy path: enqueues pull",
			ctx:              context.Background(),
			setup:            readyWithBare,
			repoConfig:       defaultRepoConfig,
			request:          model.RepoOperationRequest{Name: "r", Body: model.RepoOperationBody{Kind: model.PullKind, Ref: "main"}},
			expectedResponse: model.RepoOperationResponse{Repo: model.RepoState{Name: "r", State: model.StateReady}},
		},
		{
			name:        "error path: not found for unknown repo",
			ctx:         context.Background(),
			repoConfig:  defaultRepoConfig,
			request:     model.RepoOperationRequest{Name: "unknown", Body: model.RepoOperationBody{Kind: model.PullKind, Ref: "main"}},
			expectedErr: errNotFound,
		},
		{
			name: "error path: conflict when repo state is init",
			ctx:  context.Background(),
			setup: func(t *testing.T, cfg *Config, s *storage.StateStore) {
				t.Helper()
				require.NoError(t, os.MkdirAll(filepath.Join(cfg.WorkDir, "r", ".bare"), 0o755))
			},
			repoConfig:  defaultRepoConfig,
			request:     model.RepoOperationRequest{Name: "r", Body: model.RepoOperationBody{Kind: model.PullKind, Ref: "main"}},
			expectedErr: errConflict,
		},
		{
			name: "error path: conflict when repo state is syncing",
			ctx:  context.Background(),
			setup: func(t *testing.T, cfg *Config, s *storage.StateStore) {
				t.Helper()
				rs, err := s.Get("r")
				require.NoError(t, err)
				rs.State = model.StateSyncing
				s.Set("r", rs)
				require.NoError(t, os.MkdirAll(filepath.Join(cfg.WorkDir, "r", ".bare"), 0o755))
			},
			repoConfig:  defaultRepoConfig,
			request:     model.RepoOperationRequest{Name: "r", Body: model.RepoOperationBody{Kind: model.PullKind, Ref: "main"}},
			expectedErr: errConflict,
		},
		{
			name: "error path: conflict when repo state is resetting",
			ctx:  context.Background(),
			setup: func(t *testing.T, cfg *Config, s *storage.StateStore) {
				t.Helper()
				rs, err := s.Get("r")
				require.NoError(t, err)
				rs.State = model.StateResetting
				s.Set("r", rs)
				require.NoError(t, os.MkdirAll(filepath.Join(cfg.WorkDir, "r", ".bare"), 0o755))
			},
			repoConfig:  defaultRepoConfig,
			request:     model.RepoOperationRequest{Name: "r", Body: model.RepoOperationBody{Kind: model.PullKind, Ref: "main"}},
			expectedErr: errConflict,
		},
		{
			name: "error path: conflict when bare dir missing",
			ctx:  context.Background(),
			setup: func(t *testing.T, cfg *Config, s *storage.StateStore) {
				t.Helper()
				rs, err := s.Get("r")
				require.NoError(t, err)
				rs.State = model.StateError
				s.Set("r", rs)
			},
			repoConfig:  defaultRepoConfig,
			request:     model.RepoOperationRequest{Name: "r", Body: model.RepoOperationBody{Kind: model.PullKind, Ref: "main"}},
			expectedErr: errConflict,
		},
		{
			name:        "error path: bad request when ref is empty",
			ctx:         context.Background(),
			setup:       readyWithBare,
			repoConfig:  defaultRepoConfig,
			request:     model.RepoOperationRequest{Name: "r", Body: model.RepoOperationBody{Kind: model.PullKind, Ref: ""}},
			expectedErr: errBadRequest,
		},
		{
			name:        "error path: bad request for unknown operation kind",
			ctx:         context.Background(),
			setup:       readyWithBare,
			repoConfig:  defaultRepoConfig,
			request:     model.RepoOperationRequest{Name: "r", Body: model.RepoOperationBody{Kind: "unknown"}},
			expectedErr: errBadRequest,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, cfg, state, workers, _ := newTestGitOpsController(t, tc.repoConfig)
			if tc.setup != nil {
				tc.setup(t, cfg, state)
			}

			resp, err := c.RepoOperation(tc.ctx, tc.request)

			if tc.expectedErr != nil {
				require.ErrorIs(t, err, tc.expectedErr)
				return
			}
			require.NoError(t, err)
			resp.Repo.Path = ""
			assert.Equal(t, tc.expectedResponse, resp)
			w := workers[tc.request.Name]
			select {
			case cmd := <-w.cmdCh:
				pull, ok := cmd.(model.PullCommand)
				require.True(t, ok, "expected PullCommand in channel")
				assert.Equal(t, tc.request.Name, pull.Name)
				assert.Equal(t, tc.request.Body.Ref, pull.Ref)
			default:
				t.Fatal("expected PullCommand to be enqueued")
			}
		})
	}
}

func TestReset(t *testing.T) {
	t.Parallel()
	repos := []model.RepoConfig{
		{Name: "r1", URL: "url1"},
		{Name: "r2", URL: "url2"},
	}

	t.Run("happy path: locks repos to resetting, wipes dirs, re-enqueues init", func(t *testing.T) {
		t.Parallel()
		c, cfg, state, workers, gits := newTestGitOpsController(t, repos)

		// Block post-reset init so we can observe StateInit before it transitions to ready.
		ctx, cancel := context.WithCancel(context.Background())
		for _, repo := range repos {
			git := gits[repo.Name]
			git.On("BareClone", mock.Anything, repo.URL, mock.Anything).
				Run(func(mock.Arguments) { <-ctx.Done() }).
				Return(context.Canceled)
		}

		var wg sync.WaitGroup
		for _, w := range workers {
			wg.Add(1)
			go func(w *Worker) { defer wg.Done(); w.Run(ctx) }(w)
		}
		t.Cleanup(func() { cancel(); wg.Wait() })

		// Create per-repo sentinel files that should be removed by reset.
		for _, repo := range repos {
			dir := filepath.Join(cfg.WorkDir, repo.Name)
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "sentinel.txt"), []byte("data"), 0o644))
		}

		resp, err := c.Reset(context.Background())
		require.NoError(t, err)

		// Response must contain all repos in StateResetting.
		require.Len(t, resp.Repos, len(repos))
		for _, rs := range resp.Repos {
			assert.Equal(t, model.StateResetting, rs.State)
		}

		// Root dirs must survive.
		assert.DirExists(t, cfg.WorkDir)
		assert.DirExists(t, cfg.WorkspaceDir)

		// Wait for workers to drain the ResetCommand; state must reach StateInit
		// (init is blocked on ctx so it cannot progress further).
		require.Eventually(t, func() bool {
			for _, repo := range repos {
				rs, err := state.Get(repo.Name)
				if err != nil || rs.State != model.StateInit {
					return false
				}
			}
			return true
		}, 5*time.Second, 10*time.Millisecond)

		// Per-repo work dirs must be gone.
		for _, repo := range repos {
			assert.NoDirExists(t, filepath.Join(cfg.WorkDir, repo.Name))
		}
	})

	t.Run("error path: conflict when reset already in progress", func(t *testing.T) {
		t.Parallel()
		c, _, state, _, _ := newTestGitOpsController(t, repos)

		// Manually put all repos into StateResetting to simulate an in-flight reset.
		for _, repo := range repos {
			rs, err := state.Get(repo.Name)
			require.NoError(t, err)
			rs.State = model.StateResetting
			state.Set(repo.Name, rs)
		}

		_, err := c.Reset(context.Background())
		require.ErrorIs(t, err, errConflict)
	})
}
