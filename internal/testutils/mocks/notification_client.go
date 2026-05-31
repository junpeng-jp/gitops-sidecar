package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
)

type MockNotificationClient struct {
	mock.Mock
}

func (m *MockNotificationClient) Notify(ctx context.Context, events []model.RepoChangedEvent) error {
	args := m.Called(ctx, events)

	return args.Error(0)
}
