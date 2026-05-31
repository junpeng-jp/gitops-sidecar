package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateStore_Init(t *testing.T) {
	testCases := []struct {
		name         string
		repos        []model.RepoConfig
		workspaceDir string
		expectedInit map[string]model.RepoState
	}{
		{
			name:         "single repo",
			repos:        []model.RepoConfig{{Name: "ha-config", URL: "u"}},
			workspaceDir: "/ws",
			expectedInit: map[string]model.RepoState{
				"ha-config": {Name: "ha-config", State: model.StateInit, Ref: "", Path: "/ws/ha-config"},
			},
		},
		{
			name: "multiple repos",
			repos: []model.RepoConfig{
				{Name: "repo-a", URL: "u"},
				{Name: "repo-b", URL: "u"},
			},
			workspaceDir: "/workspace",
			expectedInit: map[string]model.RepoState{
				"repo-a": {Name: "repo-a", State: model.StateInit, Ref: "", Path: filepath.Join("/workspace", "repo-a")},
				"repo-b": {Name: "repo-b", State: model.StateInit, Ref: "", Path: filepath.Join("/workspace", "repo-b")},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var s StateStore
			s.Init(tc.repos, tc.workspaceDir)
			for name, expected := range tc.expectedInit {
				got, ok := s.Get(name)
				require.True(t, ok, "repo %q not found", name)
				assert.Equal(t, expected.Name, got.Name)
				assert.Equal(t, expected.State, got.State)
				assert.Equal(t, expected.Ref, got.Ref)
				assert.Equal(t, expected.Path, got.Path)
				assert.Nil(t, got.LastUpdatedAt)
				assert.Empty(t, got.Error)
			}
		})
	}
}

func TestStateStore_Get(t *testing.T) {
	var s StateStore
	s.Init([]model.RepoConfig{{Name: "ha-config", URL: "u"}}, "/ws")

	testCases := []struct {
		name          string
		lookupName    string
		expectFound   bool
		expectedState model.RepoStateKind
	}{
		{
			name:          "known name",
			lookupName:    "ha-config",
			expectFound:   true,
			expectedState: model.StateInit,
		},
		{
			name:        "unknown name",
			lookupName:  "does-not-exist",
			expectFound: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rs, ok := s.Get(tc.lookupName)
			assert.Equal(t, tc.expectFound, ok)
			if tc.expectFound {
				assert.Equal(t, tc.expectedState, rs.State)
			}
		})
	}
}

func TestStateStore_SetAndGet(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	testCases := []struct {
		name          string
		setState      model.RepoState
		expectedState model.RepoState
	}{
		{
			name: "set ready with LastUpdatedAt",
			setState: model.RepoState{
				State:         model.StateReady,
				Ref:           "main",
				Path:          "/ws/ha-config",
				LastUpdatedAt: &now,
			},
			expectedState: model.RepoState{
				State:         model.StateReady,
				Ref:           "main",
				Path:          "/ws/ha-config",
				LastUpdatedAt: &now,
			},
		},
		{
			name: "set error with Error field",
			setState: model.RepoState{
				State: model.StateError,
				Ref:   "main",
				Path:  "/ws/ha-config",
				Error: "clone failed",
			},
			expectedState: model.RepoState{
				State: model.StateError,
				Ref:   "main",
				Path:  "/ws/ha-config",
				Error: "clone failed",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var s StateStore
			s.Init([]model.RepoConfig{{Name: "ha-config", URL: "u"}}, "/ws")
			s.Set("ha-config", tc.setState)
			got, ok := s.Get("ha-config")
			require.True(t, ok)
			assert.Equal(t, tc.expectedState.State, got.State)
			assert.Equal(t, tc.expectedState.Ref, got.Ref)
			assert.Equal(t, tc.expectedState.Path, got.Path)
			if tc.expectedState.LastUpdatedAt != nil {
				require.NotNil(t, got.LastUpdatedAt)
				assert.Equal(t, *tc.expectedState.LastUpdatedAt, *got.LastUpdatedAt)
			} else {
				assert.Nil(t, got.LastUpdatedAt)
			}
			assert.Equal(t, tc.expectedState.Error, got.Error)
		})
	}
}

func TestStateStore_SetAll(t *testing.T) {
	now := time.Now().UTC()

	testCases := []struct {
		name        string
		targetState model.RepoStateKind
	}{
		{
			name:        "mix of states reset to syncing",
			targetState: model.StateSyncing,
		},
		{
			name:        "mix of states reset to init",
			targetState: model.StateInit,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var s StateStore
			repos := []model.RepoConfig{
				{Name: "repo-a", URL: "u"},
				{Name: "repo-b", URL: "u"},
				{Name: "repo-c", URL: "u"},
			}
			s.Init(repos, "/ws")
			s.Set("repo-a", model.RepoState{State: model.StateReady, Ref: "main", Path: "/ws/repo-a", LastUpdatedAt: &now})
			s.Set("repo-b", model.RepoState{State: model.StateError, Ref: "main", Path: "/ws/repo-b", Error: "boom"})
			s.Set("repo-c", model.RepoState{State: model.StateSyncing, Ref: "main", Path: "/ws/repo-c"})

			s.SetAll(tc.targetState)

			all := s.List(100)
			for _, rs := range all {
				assert.Equal(t, tc.targetState, rs.State, "repo %q state", rs.Name)
				assert.Empty(t, rs.Error, "repo %q Error should be nil", rs.Name)
				assert.Nil(t, rs.LastUpdatedAt, "repo %q LastUpdatedAt should be nil", rs.Name)
			}
		})
	}
}

func TestStateStore_List(t *testing.T) {
	testCases := []struct {
		name          string
		repos         []model.RepoConfig
		limit         int
		expectedNames []string
	}{
		{
			name: "returns all repos sorted alphabetically when under limit",
			repos: []model.RepoConfig{
				{Name: "repo-c", URL: "u"},
				{Name: "repo-a", URL: "u"},
				{Name: "repo-b", URL: "u"},
			},
			limit:         10,
			expectedNames: []string{"repo-a", "repo-b", "repo-c"},
		},
		{
			name: "limit caps the number of results",
			repos: []model.RepoConfig{
				{Name: "repo-a", URL: "u"},
				{Name: "repo-b", URL: "u"},
				{Name: "repo-c", URL: "u"},
			},
			limit:         2,
			expectedNames: []string{"repo-a", "repo-b"},
		},
		{
			name: "limit larger than repo count returns all",
			repos: []model.RepoConfig{
				{Name: "repo-a", URL: "u"},
			},
			limit:         100,
			expectedNames: []string{"repo-a"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var s StateStore
			s.Init(tc.repos, "/ws")
			got := s.List(tc.limit)
			require.Len(t, got, len(tc.expectedNames))
			for i, name := range tc.expectedNames {
				assert.Equal(t, name, got[i].Name)
			}
		})
	}
}
