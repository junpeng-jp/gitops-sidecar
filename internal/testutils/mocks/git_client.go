package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"
)

type MockGitClient struct {
	mock.Mock
}

func (m *MockGitClient) BareClone(ctx context.Context, url, dest string) error {
	return m.Called(ctx, url, dest).Error(0)
}

func (m *MockGitClient) Fetch(ctx context.Context, bareDir string) error {
	return m.Called(ctx, bareDir).Error(0)
}

func (m *MockGitClient) WorktreePrune(ctx context.Context, bareDir string) error {
	return m.Called(ctx, bareDir).Error(0)
}

func (m *MockGitClient) WorktreeAdd(ctx context.Context, bareDir, dest, ref string) error {
	return m.Called(ctx, bareDir, dest, ref).Error(0)
}

func (m *MockGitClient) VerifyCommit(ctx context.Context, worktreeDir string) error {
	return m.Called(ctx, worktreeDir).Error(0)
}
