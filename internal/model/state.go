package model

import "time"

type RepoStateKind string

const (
	StateInit      RepoStateKind = "init"
	StateSyncing   RepoStateKind = "syncing"
	StateReady     RepoStateKind = "ready"
	StateError     RepoStateKind = "error"
	StateResetting RepoStateKind = "resetting"
)

type RepoState struct {
	Name          string        `json:"name"`
	State         RepoStateKind `json:"state"`
	Ref           string        `json:"ref"`
	Path          string        `json:"path"`
	LastUpdatedAt *time.Time    `json:"last_updated_at"`
	Error         string        `json:"error,omitempty"`
}

type GetReposResponse struct {
	Repos []RepoState `json:"repos"`
}

type GetRepoResponse struct {
	Repo RepoState `json:"repo"`
}

type NotificationEventKind string

const (
	EventKindRepoChanged NotificationEventKind = "repo_changed"
)

// RepoChangedEvent is the webhook payload for any repo state transition.
// POST /reset fires one event per repo.
type RepoChangedEvent struct {
	EventKind     NotificationEventKind `json:"event_kind"`
	Name          string                `json:"name"`
	State         RepoStateKind         `json:"state"`
	PreviousState RepoStateKind         `json:"previous_state"`
	Ref           string                `json:"ref"`
	LastUpdatedAt *time.Time            `json:"last_updated_at"`
	Error         string                `json:"error,omitempty"`
}
