package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
)

var (
	errMissingWorkspaceDir      = errors.New("workspaceDir is required")
	errNoRepos                  = errors.New("at least one repo is required")
	errInvalidRepoName          = errors.New("invalid repo name")
	errMissingRepoURL           = errors.New("repo url is required")
	errUnsupportedNotification  = errors.New("unsupported notification type")
	errMissingNotificationURL   = errors.New("notification url is required")
	errInvalidMaxBatchSize      = errors.New("maxBatchSize must be greater than 0")
	errInvalidBatchInterval     = errors.New("batchInterval must be greater than 0")
	errRuntimeDirMissing        = errors.New("runtimeDir does not exist")
	errRuntimeFileMissing       = errors.New("missing required SSH runtime file")
	errMissingAllowedSigners    = errors.New("missing allowed-signers file for repo with verifyCommit")
)

const (
	defaultWorkDir       = "/tmp/gitops"
	defaultConfigFile    = "/etc/gitops/config.json"
	defaultPort          = "9001"
	defaultMaxBatchSize  = 16
	defaultBatchInterval = 3 * time.Second
)

var repoNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

type Config struct {
	RuntimeDir   string                    `json:"runtimeDir"`
	WorkDir      string                    `json:"workDir"`
	WorkspaceDir string                    `json:"workspaceDir"`
	Repos        []model.RepoConfig        `json:"repos"`
	Notification *model.NotificationConfig `json:"notification"`
	Port         string                    `json:"-"`
}

func LoadConfig() (*Config, error) {
	configFile := os.Getenv("GITOPS_CONFIG_FILE")
	if configFile == "" {
		configFile = defaultConfigFile
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", configFile, err)
	}

	cfg := &Config{
		WorkDir: defaultWorkDir,
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.WorkDir == "" {
		cfg.WorkDir = defaultWorkDir
	}

	if cfg.WorkspaceDir == "" {
		return nil, fmt.Errorf("config: %w", errMissingWorkspaceDir)
	}
	if len(cfg.Repos) == 0 {
		return nil, fmt.Errorf("config: %w", errNoRepos)
	}
	for _, r := range cfg.Repos {
		if err := validateRepoConfig(r); err != nil {
			return nil, err
		}
	}
	if cfg.Notification != nil {
		if cfg.Notification.Type != "ha-webhook" {
			return nil, fmt.Errorf("config: %w: %q", errUnsupportedNotification, cfg.Notification.Type)
		}
		if cfg.Notification.URL == "" {
			return nil, fmt.Errorf("config: %w", errMissingNotificationURL)
		}
		if cfg.Notification.MaxBatchSize < 0 {
			return nil, fmt.Errorf("config: %w", errInvalidMaxBatchSize)
		}
		if cfg.Notification.MaxBatchSize == 0 {
			cfg.Notification.MaxBatchSize = defaultMaxBatchSize
		}
		if cfg.Notification.BatchInterval < 0 {
			return nil, fmt.Errorf("config: %w", errInvalidBatchInterval)
		}
		if cfg.Notification.BatchInterval == 0 {
			cfg.Notification.BatchInterval = model.Duration(defaultBatchInterval)
		}
	}

	if cfg.RuntimeDir != "" {
		if err := validateSSHRuntime(cfg.RuntimeDir, cfg.Repos); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
	}

	cfg.Port = os.Getenv("GITOPS_PORT")
	if cfg.Port == "" {
		cfg.Port = defaultPort
	}

	return cfg, nil
}

func isSSHURL(url string) bool {
	return strings.HasPrefix(url, "git@") || strings.HasPrefix(url, "ssh://")
}

func validateSSHRuntime(runtimeDir string, repos []model.RepoConfig) error {
	if _, err := os.Stat(runtimeDir); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", errRuntimeDirMissing, runtimeDir)
	} else if err != nil {
		return fmt.Errorf("stat runtimeDir %s: %w", runtimeDir, err)
	}

	// gitconfig is always required when a runtimeDir is configured.
	if p := filepath.Join(runtimeDir, "gitconfig"); true {
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", errRuntimeFileMissing, p)
		} else if err != nil {
			return fmt.Errorf("stat %s: %w", p, err)
		}
	}

	// ssh_config and known_hosts are only needed for SSH-protocol repos.
	if slices.ContainsFunc(repos, func(r model.RepoConfig) bool { return isSSHURL(r.URL) }) {
		for _, name := range []string{"ssh_config", "known_hosts"} {
			p := filepath.Join(runtimeDir, name)
			if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("%w: %s", errRuntimeFileMissing, p)
			} else if err != nil {
				return fmt.Errorf("stat %s: %w", p, err)
			}
		}
	}

	for _, repo := range repos {
		if repo.VerifyCommit {
			p := filepath.Join(runtimeDir, "allowed-signers", repo.Name)
			if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("%w: %s", errMissingAllowedSigners, p)
			} else if err != nil {
				return fmt.Errorf("stat allowed-signers %s: %w", p, err)
			}
		}
	}

	return nil
}

func validateRepoConfig(r model.RepoConfig) error {
	if len(r.Name) > 64 || !repoNamePattern.MatchString(r.Name) {
		return fmt.Errorf("config: %w: %q", errInvalidRepoName, r.Name)
	}
	if r.URL == "" {
		return fmt.Errorf("config: repo %q: %w", r.Name, errMissingRepoURL)
	}
	return nil
}
