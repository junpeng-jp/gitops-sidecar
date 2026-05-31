package model

import (
	"encoding/json"
	"time"
)

// Duration is a time.Duration that marshals/unmarshals as a Go duration string (e.g. "3s", "500ms").
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
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
	Name         string `json:"name"`
	URL          string `json:"url"`
	VerifyCommit bool   `json:"verifyCommit"`
}

type NotificationConfig struct {
	Type          string   `json:"type"`
	URL           string   `json:"url"`
	MaxBatchSize  int      `json:"maxBatchSize"`
	BatchInterval Duration `json:"batchInterval"`
}
