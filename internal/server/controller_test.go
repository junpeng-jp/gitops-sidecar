package server

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
	"github.com/junpeng-jp/gitops-sidecar/internal/storage"
	"github.com/junpeng-jp/gitops-sidecar/internal/testutils/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestGitOpsController(t *testing.T, repos []model.RepoConfig) (*GitOpsController, *Config, *storage.StateStore, *Worker) {
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
	c := &GitOpsController{cfg: cfg, state: state, engine: engine, log: slog.Default()}
	return c, cfg, state, engine
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
			c, _, _, _ := newTestGitOpsController(t, repos)
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
			c, _, _, _ := newTestGitOpsController(t, repos)
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
			c, cfg, state, engine := newTestGitOpsController(t, tc.repoConfig)
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
			select {
			case cmd := <-engine.cmdCh:
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
	defaultRepoConfig := []model.RepoConfig{
		{Name: "r1", URL: "url1"},
		{Name: "r2", URL: "url2"},
	}

	testCases := []struct {
		name             string
		ctx              context.Context
		repoConfig       []model.RepoConfig
		expectedErr      error
		expectedResponse model.ResetResponse
	}{
		{
			name:             "happy path: wipes dirs and re-enqueues init commands",
			ctx:              context.Background(),
			repoConfig:       defaultRepoConfig,
			expectedResponse: model.ResetResponse{Success: true},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, cfg, state, engine := newTestGitOpsController(t, tc.repoConfig)

			sentinel := filepath.Join(cfg.WorkDir, "sentinel.txt")
			require.NoError(t, os.WriteFile(sentinel, []byte("data"), 0o644))

			resp, err := c.Reset(tc.ctx)

			if tc.expectedErr != nil {
				require.ErrorIs(t, err, tc.expectedErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expectedResponse, resp)
			assert.NoFileExists(t, sentinel)
			assert.DirExists(t, cfg.WorkDir)
			assert.DirExists(t, cfg.WorkspaceDir)
			for _, repo := range tc.repoConfig {
				rs, err := state.Get(repo.Name)
				require.NoError(t, err)
				assert.Equal(t, model.StateInit, rs.State)
			}
			var initCmds []model.Command
			for len(engine.cmdCh) > 0 {
				initCmds = append(initCmds, <-engine.cmdCh)
			}
			require.Len(t, initCmds, len(tc.repoConfig))
			for _, cmd := range initCmds {
				_, ok := cmd.(model.InitCommand)
				assert.True(t, ok, "expected InitCommand")
			}
		})
	}
}
