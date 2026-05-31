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
	errMissingWorkspaceDir     = errors.New("workspaceDir is required")
	errNoRepos                 = errors.New("at least one repo is required")
	errInvalidRepoName         = errors.New("invalid repo name")
	errMissingRepoURL          = errors.New("repo url is required")
	errUnsupportedNotification = errors.New("unsupported notification type")
	errMissingNotificationURL  = errors.New("notification url is required")
	errInvalidMaxBatchSize     = errors.New("maxBatchSize must be greater than 0")
	errInvalidBatchInterval    = errors.New("batchInterval must be greater than 0")
	errRuntimeDirMissing       = errors.New("runtimeDir does not exist")
	errRuntimeFileMissing      = errors.New("missing required runtime file")
	errMissingAllowedSigners   = errors.New("missing allowed-signers file for repo with verifyCommit")
)

const (
	defaultWorkDir               = "/tmp/gitops"
	defaultConfigFile            = "/etc/gitops/config.json"
	defaultPort                  = "57005"
	defaultCommandQueueSize      = 16
	defaultNotificationQueueSize = 64
	defaultMaxBatchSize          = 16
	defaultBatchInterval         = 3 * time.Second
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

	data, err := os.ReadFile(configFile) //nolint:gosec // path comes from trusted env var
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", configFile, err)
	}

	cfg := &Config{
		WorkDir: defaultWorkDir,
	}
	err = json.Unmarshal(data, cfg)
	if err != nil {
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
	for i, r := range cfg.Repos {
		err := validateRepoConfig(r)
		if err != nil {
			return nil, err
		}
		if cfg.Repos[i].CommandQueueSize == 0 {
			cfg.Repos[i].CommandQueueSize = defaultCommandQueueSize
		}
	}
	if cfg.Notification != nil {
		err = applyNotificationDefaults(cfg.Notification)
		if err != nil {
			return nil, err
		}
	}

	if cfg.RuntimeDir != "" {
		err = validateRuntimeDir(cfg.RuntimeDir, cfg.Repos)
		if err != nil {
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

func statRequired(path string, missingErr error) error {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", missingErr, path)
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	return nil
}

func validateRuntimeDir(runtimeDir string, repos []model.RepoConfig) error {
	err := statRequired(runtimeDir, errRuntimeDirMissing)
	if err != nil {
		return err
	}

	err = statRequired(filepath.Join(runtimeDir, "gitconfig"), errRuntimeFileMissing)
	if err != nil {
		return err
	}

	// ssh_config and known_hosts are only needed for SSH-protocol repos.
	if slices.ContainsFunc(repos, func(r model.RepoConfig) bool { return isSSHURL(r.URL) }) {
		for _, name := range []string{"ssh_config", "known_hosts"} {
			err = statRequired(filepath.Join(runtimeDir, name), errRuntimeFileMissing)
			if err != nil {
				return err
			}
		}
	}

	for _, repo := range repos {
		if repo.VerifyCommit {
			p := filepath.Join(runtimeDir, repo.Name, "allowed-signers")
			err = statRequired(p, errMissingAllowedSigners)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func applyNotificationDefaults(n *model.NotificationConfig) error {
	if n.Type == model.NotificationTypeUnknown {
		return fmt.Errorf("config: %w", errUnsupportedNotification)
	}
	if n.URL == "" {
		return fmt.Errorf("config: %w", errMissingNotificationURL)
	}
	if n.MaxBatchSize < 0 {
		return fmt.Errorf("config: %w", errInvalidMaxBatchSize)
	}
	if n.BatchInterval < 0 {
		return fmt.Errorf("config: %w", errInvalidBatchInterval)
	}
	if n.QueueSize == 0 {
		n.QueueSize = defaultNotificationQueueSize
	}
	if n.MaxBatchSize == 0 {
		n.MaxBatchSize = defaultMaxBatchSize
	}
	if n.BatchInterval == 0 {
		n.BatchInterval = model.Duration(defaultBatchInterval)
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
