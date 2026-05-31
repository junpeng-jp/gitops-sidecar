package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	haConfig   = "ha-config"
	repoAName  = "repo-a"
	repoBName  = "repo-b"
	repoCName  = "repo-c"
	dummyURL   = "u"
	wsPath     = "/ws"
	wsHAConfig = "/ws/ha-config"
	refMain    = "main"
)

func TestStateStore_Init(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name         string
		repos        []model.RepoConfig
		workspaceDir string
		expectedInit map[string]model.RepoState
	}{
		{
			name:         "single repo",
			repos:        []model.RepoConfig{{Name: haConfig, URL: dummyURL}},
			workspaceDir: wsPath,
			expectedInit: map[string]model.RepoState{
				haConfig: {Name: haConfig, State: model.StateInit, Ref: "", Path: wsHAConfig},
			},
		},
		{
			name: "multiple repos",
			repos: []model.RepoConfig{
				{Name: repoAName, URL: dummyURL},
				{Name: repoBName, URL: dummyURL},
			},
			workspaceDir: "/workspace",
			expectedInit: map[string]model.RepoState{
				repoAName: {Name: repoAName, State: model.StateInit, Ref: "", Path: filepath.Join("/workspace", repoAName)},
				repoBName: {Name: repoBName, State: model.StateInit, Ref: "", Path: filepath.Join("/workspace", repoBName)},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
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
	t.Parallel()
	var s StateStore
	s.Init([]model.RepoConfig{{Name: haConfig, URL: dummyURL}}, wsPath)

	testCases := []struct {
		name          string
		lookupName    string
		expectFound   bool
		expectedState model.RepoStateKind
	}{
		{
			name:          "known name",
			lookupName:    haConfig,
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
			t.Parallel()
			rs, ok := s.Get(tc.lookupName)
			assert.Equal(t, tc.expectFound, ok)
			if tc.expectFound {
				assert.Equal(t, tc.expectedState, rs.State)
			}
		})
	}
}

func TestStateStore_SetAndGet(t *testing.T) {
	t.Parallel()
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
				Ref:           refMain,
				Path:          wsHAConfig,
				LastUpdatedAt: &now,
			},
			expectedState: model.RepoState{
				State:         model.StateReady,
				Ref:           refMain,
				Path:          wsHAConfig,
				LastUpdatedAt: &now,
			},
		},
		{
			name: "set error with Error field",
			setState: model.RepoState{
				State: model.StateError,
				Ref:   refMain,
				Path:  wsHAConfig,
				Error: "clone failed",
			},
			expectedState: model.RepoState{
				State: model.StateError,
				Ref:   refMain,
				Path:  wsHAConfig,
				Error: "clone failed",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var s StateStore
			s.Init([]model.RepoConfig{{Name: haConfig, URL: dummyURL}}, wsPath)
			s.Set(haConfig, tc.setState)
			got, ok := s.Get(haConfig)
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
	t.Parallel()
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
			t.Parallel()
			var s StateStore
			repos := []model.RepoConfig{
				{Name: repoAName, URL: dummyURL},
				{Name: repoBName, URL: dummyURL},
				{Name: repoCName, URL: dummyURL},
			}
			s.Init(repos, wsPath)
			s.Set(repoAName, model.RepoState{State: model.StateReady, Ref: refMain, Path: filepath.Join(wsPath, repoAName), LastUpdatedAt: &now})
			s.Set(repoBName, model.RepoState{State: model.StateError, Ref: refMain, Path: filepath.Join(wsPath, repoBName), Error: "boom"})
			s.Set(repoCName, model.RepoState{State: model.StateSyncing, Ref: refMain, Path: filepath.Join(wsPath, repoCName)})

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
	t.Parallel()
	testCases := []struct {
		name          string
		repos         []model.RepoConfig
		limit         int
		expectedNames []string
	}{
		{
			name: "returns all repos sorted alphabetically when under limit",
			repos: []model.RepoConfig{
				{Name: repoCName, URL: dummyURL},
				{Name: repoAName, URL: dummyURL},
				{Name: repoBName, URL: dummyURL},
			},
			limit:         10,
			expectedNames: []string{repoAName, repoBName, repoCName},
		},
		{
			name: "limit caps the number of results",
			repos: []model.RepoConfig{
				{Name: repoAName, URL: dummyURL},
				{Name: repoBName, URL: dummyURL},
				{Name: repoCName, URL: dummyURL},
			},
			limit:         2,
			expectedNames: []string{repoAName, repoBName},
		},
		{
			name: "limit larger than repo count returns all",
			repos: []model.RepoConfig{
				{Name: repoAName, URL: dummyURL},
			},
			limit:         100,
			expectedNames: []string{repoAName},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var s StateStore
			s.Init(tc.repos, wsPath)
			got := s.List(tc.limit)
			require.Len(t, got, len(tc.expectedNames))
			for i, name := range tc.expectedNames {
				assert.Equal(t, name, got[i].Name)
			}
		})
	}
}
