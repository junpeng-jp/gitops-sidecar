package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHomeAssistantNotificationWebhook_Notify(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		url            string
		serverStatus   int
		events         []model.RepoChangedEvent
		expectedEvents []model.RepoChangedEvent
		expectError    bool
	}{
		{
			name:         "happy path: sends JSON batch with correct content type",
			serverStatus: http.StatusOK,
			events: []model.RepoChangedEvent{
				{
					EventKind:     model.EventKindRepoChanged,
					Name:          "r",
					State:         model.StateReady,
					PreviousState: model.StateSyncing,
					Ref:           "main",
				},
			},
			expectedEvents: []model.RepoChangedEvent{
				{
					EventKind:     model.EventKindRepoChanged,
					Name:          "r",
					State:         model.StateReady,
					PreviousState: model.StateSyncing,
					Ref:           "main",
				},
			},
		},
		{
			name:         "happy path: sends multiple events in one batch",
			serverStatus: http.StatusOK,
			events: []model.RepoChangedEvent{
				{EventKind: model.EventKindRepoChanged, Name: "r1", State: model.StateReady},
				{EventKind: model.EventKindRepoChanged, Name: "r2", State: model.StateError},
			},
			expectedEvents: []model.RepoChangedEvent{
				{EventKind: model.EventKindRepoChanged, Name: "r1", State: model.StateReady},
				{EventKind: model.EventKindRepoChanged, Name: "r2", State: model.StateError},
			},
		},
		{
			name:         "error path: server error does not panic",
			serverStatus: http.StatusInternalServerError,
			events:       []model.RepoChangedEvent{{EventKind: model.EventKindRepoChanged, Name: "r", State: model.StateReady}},
		},
		{
			name:        "error path: connection refused returns error",
			url:         "http://127.0.0.1:1",
			events:      []model.RepoChangedEvent{{EventKind: model.EventKindRepoChanged, Name: "r", State: model.StateReady}},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotBody []byte
			var gotContentType string

			url := tc.url
			if url == "" {
				status := tc.serverStatus
				if status == 0 {
					status = http.StatusOK
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotBody, _ = io.ReadAll(r.Body)
					gotContentType = r.Header.Get("Content-Type")
					w.WriteHeader(status)
				}))
				t.Cleanup(srv.Close)
				url = srv.URL
			}

			wh := NewHomeAssistantNotificationWebhook(url)
			err := wh.Notify(context.Background(), tc.events)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tc.expectedEvents != nil {
				require.NotEmpty(t, gotBody)
				assert.Equal(t, "application/json", gotContentType)

				var decoded struct {
					Updates []model.RepoChangedEvent `json:"updates"`
				}
				require.NoError(t, json.Unmarshal(gotBody, &decoded))
				require.Len(t, decoded.Updates, len(tc.expectedEvents))
				for i, want := range tc.expectedEvents {
					got := decoded.Updates[i]
					assert.Equal(t, want.EventKind, got.EventKind)
					assert.Equal(t, want.Name, got.Name)
					assert.Equal(t, want.State, got.State)
					assert.Equal(t, want.PreviousState, got.PreviousState)
					assert.Equal(t, want.Ref, got.Ref)
					assert.Empty(t, got.Error)
				}
			}
		})
	}
}
