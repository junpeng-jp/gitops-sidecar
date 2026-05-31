package client

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
)

type NotificationClient interface {
	Notify(ctx context.Context, events []model.RepoChangedEvent) error
}

const defaultHTTPClientTimeout = 10 * time.Second

type HomeAssistantNotificationWebhook struct {
	url        string
	httpClient *http.Client
}

func NewHomeAssistantNotificationWebhook(url string) *HomeAssistantNotificationWebhook {
	return &HomeAssistantNotificationWebhook{
		url:        url,
		httpClient: &http.Client{Timeout: defaultHTTPClientTimeout},
	}
}

func (h *HomeAssistantNotificationWebhook) Notify(ctx context.Context, events []model.RepoChangedEvent) error {
	body, err := json.Marshal(struct {
		Updates []model.RepoChangedEvent `json:"updates"`
	}{Updates: events})
	if err != nil {
		return err
	}

	return h.post(ctx, body)
}

func (h *HomeAssistantNotificationWebhook) post(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}
