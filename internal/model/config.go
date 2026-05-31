package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var errUnknownNotificationType = errors.New("unknown notification type")

// Duration is a time.Duration that marshals/unmarshals as a Go duration string (e.g. "3s", "500ms").
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	err := json.Unmarshal(b, &s)
	if err != nil {
		return err
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(dur)

	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

type RepoConfig struct {
	Name             string `json:"name"`
	URL              string `json:"url"`
	VerifyCommit     bool   `json:"verifyCommit"`
	CommandQueueSize int    `json:"commandQueueSize"`
}

type NotificationType int

const (
	NotificationTypeUnknown NotificationType = iota
	NotificationTypeHAWebhook
)

func (t *NotificationType) UnmarshalJSON(b []byte) error {
	var s string
	err := json.Unmarshal(b, &s)
	if err != nil {
		return err
	}
	switch s {
	case "ha-webhook":
		*t = NotificationTypeHAWebhook
	default:
		*t = NotificationTypeUnknown
	}

	return nil
}

func (t NotificationType) MarshalJSON() ([]byte, error) {
	switch t {
	case NotificationTypeUnknown:
		return nil, fmt.Errorf("%w: %d", errUnknownNotificationType, int(t))
	case NotificationTypeHAWebhook:
		return json.Marshal("ha-webhook")
	}

	return nil, fmt.Errorf("%w: %d", errUnknownNotificationType, int(t))
}

type NotificationConfig struct {
	Type          NotificationType `json:"type"`
	URL           string           `json:"url"`
	QueueSize     int              `json:"queueSize"`
	MaxBatchSize  int              `json:"maxBatchSize"`
	BatchInterval Duration         `json:"batchInterval"`
}
