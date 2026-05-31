package storage

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
)

var ErrRepoNotFound = errors.New("repo not found")

type StateStore struct {
	mu    sync.RWMutex
	repos map[string]model.RepoState
}

func (s *StateStore) Init(repos []model.RepoConfig, workspaceDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repos = make(map[string]model.RepoState, len(repos))
	for _, r := range repos {
		s.repos[r.Name] = model.RepoState{
			Name:  r.Name,
			State: model.StateInit,
			Path:  filepath.Join(workspaceDir, r.Name),
		}
	}
}

func (s *StateStore) Get(name string) (model.RepoState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rs, ok := s.repos[name]
	if !ok {
		return model.RepoState{}, ErrRepoNotFound
	}
	return rs, nil
}

func (s *StateStore) Set(name string, rs model.RepoState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repos[name] = rs
}

func (s *StateStore) List(limit int) []model.RepoState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.RepoState, 0, len(s.repos))
	for _, v := range s.repos {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b model.RepoState) int {
		return strings.Compare(a.Name, b.Name)
	})
	if limit < len(out) {
		out = out[:limit]
	}
	return out
}
func (s *StateStore) LockAll() ([]model.RepoState, map[string]model.RepoStateKind, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rs := range s.repos {
		if rs.State == model.StateResetting {
			return nil, nil, fmt.Errorf("reset already in progress")
		}
	}
	prev := make(map[string]model.RepoStateKind, len(s.repos))
	out := make([]model.RepoState, 0, len(s.repos))
	for name, rs := range s.repos {
		prev[name] = rs.State
		rs.State = model.StateResetting
		rs.Error = ""
		rs.LastUpdatedAt = nil
		s.repos[name] = rs
		out = append(out, rs)
	}
	slices.SortFunc(out, func(a, b model.RepoState) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out, prev, nil
}

func (s *StateStore) SetAll(state model.RepoStateKind) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, rs := range s.repos {
		rs.State = state
		rs.Ref = ""
		rs.Error = ""
		rs.LastUpdatedAt = nil
		s.repos[name] = rs
	}
}
