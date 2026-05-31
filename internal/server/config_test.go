package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/junpeng-jp/gitops-sidecar/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfigFile(t *testing.T, cfg any) string {
	t.Helper()
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, data, 0600))
	return path
}

// writeRuntimeDir creates a temp dir populated with the required runtime files.
// ssh_config and known_hosts are only written when at least one repo uses an SSH URL.
// Repos with VerifyCommit get a corresponding allowed-signers file.
func writeRuntimeDir(t *testing.T, repos []model.RepoConfig) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gitconfig"), []byte(""), 0600))
	for _, r := range repos {
		if isSSHURL(r.URL) {
			for _, name := range []string{"ssh_config", "known_hosts"} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(""), 0600))
			}
			break
		}
	}
	for _, repo := range repos {
		if repo.VerifyCommit {
			signerDir := filepath.Join(dir, "allowed-signers")
			require.NoError(t, os.MkdirAll(signerDir, 0755))
			require.NoError(t, os.WriteFile(filepath.Join(signerDir, repo.Name), []byte(""), 0600))
		}
	}
	return dir
}

func TestLoadConfig(t *testing.T) {
	validRepo := model.RepoConfig{Name: "ha-config", URL: "git@github.com:org/ha-config.git"}
	validRepoWithDefaults := model.RepoConfig{Name: "ha-config", URL: "git@github.com:org/ha-config.git", CommandQueueSize: 16}
	defaultConfig := Config{
		Port:         "9001",
		WorkDir:      "/tmp/gitops",
		WorkspaceDir: "/config/.gitops",
		Repos:        []model.RepoConfig{validRepoWithDefaults},
	}

	testCases := []struct {
		name           string
		configJSON     any
		envPort        string
		expectedErr    error
		expectedConfig *Config
	}{
		{
			name: "valid minimal config applies defaults",
			configJSON: map[string]any{
				"workspaceDir": "/config/.gitops",
				"repos":        []model.RepoConfig{validRepo},
			},
			expectedConfig: &defaultConfig,
		},
		{
			name: "valid config with notification applies notification defaults",
			configJSON: map[string]any{
				"workDir":      "/tmp/custom",
				"workspaceDir": "/config/.gitops",
				"repos":        []model.RepoConfig{validRepo},
				"notification": map[string]any{"type": "ha-webhook", "url": "http://ha.local/webhook"},
			},
			expectedConfig: &Config{
				Port:         "9001",
				WorkDir:      "/tmp/custom",
				WorkspaceDir: "/config/.gitops",
				Repos:        []model.RepoConfig{validRepoWithDefaults},
				Notification: &model.NotificationConfig{Type: "ha-webhook", URL: "http://ha.local/webhook", QueueSize: 64, MaxBatchSize: 16, BatchInterval: model.Duration(3 * time.Second)},
			},
		},
		{
			name: "GITOPS_PORT overrides default port",
			configJSON: map[string]any{
				"workspaceDir": "/config/.gitops",
				"repos":        []model.RepoConfig{validRepo},
			},
			envPort: "8080",
			expectedConfig: func() *Config {
				c := defaultConfig
				c.Port = "8080"
				return &c
			}(),
		},
		{
			name: "missing workspaceDir",
			configJSON: map[string]any{
				"repos": []model.RepoConfig{validRepo},
			},
			expectedErr: errMissingWorkspaceDir,
		},
		{
			name: "no repos",
			configJSON: map[string]any{
				"workspaceDir": "/config/.gitops",
				"repos":        []model.RepoConfig{},
			},
			expectedErr: errNoRepos,
		},
		{
			name: "invalid repo name — bad pattern",
			configJSON: map[string]any{
				"workspaceDir": "/config/.gitops",
				"repos":        []model.RepoConfig{{Name: "My-Repo", URL: "u"}},
			},
			expectedErr: errInvalidRepoName,
		},
		{
			name: "invalid repo name — too long",
			configJSON: map[string]any{
				"workspaceDir": "/config/.gitops",
				"repos":        []model.RepoConfig{{Name: strings.Repeat("a", 65), URL: "u"}},
			},
			expectedErr: errInvalidRepoName,
		},
		{
			name: "missing repo url",
			configJSON: map[string]any{
				"workspaceDir": "/config/.gitops",
				"repos":        []model.RepoConfig{{Name: "ha-config", URL: ""}},
			},
			expectedErr: errMissingRepoURL,
		},
		{
			name: "negative maxBatchSize is rejected",
			configJSON: map[string]any{
				"workspaceDir": "/config/.gitops",
				"repos":        []model.RepoConfig{validRepo},
				"notification": map[string]any{"type": "ha-webhook", "url": "http://ha.local/webhook", "maxBatchSize": -1},
			},
			expectedErr: errInvalidMaxBatchSize,
		},
		{
			name: "negative batchInterval is rejected",
			configJSON: map[string]any{
				"workspaceDir": "/config/.gitops",
				"repos":        []model.RepoConfig{validRepo},
				"notification": map[string]any{"type": "ha-webhook", "url": "http://ha.local/webhook", "batchInterval": "-1s"},
			},
			expectedErr: errInvalidBatchInterval,
		},
		{
			name: "unsupported notification type",
			configJSON: map[string]any{
				"workspaceDir": "/config/.gitops",
				"repos":        []model.RepoConfig{validRepo},
				"notification": map[string]any{"type": "unknown", "url": "http://x"},
			},
			expectedErr: errUnsupportedNotification,
		},
		{
			name: "missing notification url",
			configJSON: map[string]any{
				"workspaceDir": "/config/.gitops",
				"repos":        []model.RepoConfig{validRepo},
				"notification": map[string]any{"type": "ha-webhook", "url": ""},
			},
			expectedErr: errMissingNotificationURL,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			configPath := writeConfigFile(t, tc.configJSON)
			t.Setenv("GITOPS_CONFIG_FILE", configPath)

			if tc.envPort != "" {
				t.Setenv("GITOPS_PORT", tc.envPort)
			}

			cfg, err := LoadConfig()

			if tc.expectedErr != nil {
				require.ErrorIs(t, err, tc.expectedErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expectedConfig, cfg)
		})
	}
}

func TestValidateSSHRuntime(t *testing.T) {
	t.Parallel()
	sshRepo := model.RepoConfig{Name: "ha-config", URL: "git@github.com:org/ha-config.git"}
	httpsRepo := model.RepoConfig{Name: "ha-config", URL: "https://github.com/org/ha-config.git"}
	verifyRepo := model.RepoConfig{Name: "ha-config", URL: "git@github.com:org/ha-config.git", VerifyCommit: true}

	testCases := []struct {
		name        string
		setup       func(t *testing.T) string
		repos       []model.RepoConfig
		expectedErr error
	}{
		{
			name: "valid runtime dir with SSH repo",
			setup: func(t *testing.T) string {
				t.Helper()
				return writeRuntimeDir(t, []model.RepoConfig{sshRepo})
			},
			repos: []model.RepoConfig{sshRepo},
		},
		{
			name: "HTTPS repo: ssh_config and known_hosts not required",
			setup: func(t *testing.T) string {
				t.Helper()
				// Only gitconfig — no SSH files needed for HTTPS repos.
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "gitconfig"), []byte(""), 0600))
				return dir
			},
			repos: []model.RepoConfig{httpsRepo},
		},
		{
			name: "mixed repos: SSH file required when any repo uses SSH",
			setup: func(t *testing.T) string {
				t.Helper()
				// Has gitconfig only — missing ssh_config/known_hosts.
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "gitconfig"), []byte(""), 0600))
				return dir
			},
			repos:       []model.RepoConfig{httpsRepo, sshRepo},
			expectedErr: errRuntimeFileMissing,
		},
		{
			name: "runtimeDir does not exist",
			setup: func(t *testing.T) string {
				t.Helper()
				return filepath.Join(t.TempDir(), "nonexistent")
			},
			repos:       []model.RepoConfig{sshRepo},
			expectedErr: errRuntimeDirMissing,
		},
		{
			name: "gitconfig missing",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := writeRuntimeDir(t, []model.RepoConfig{sshRepo})
				require.NoError(t, os.Remove(filepath.Join(dir, "gitconfig")))
				return dir
			},
			repos:       []model.RepoConfig{sshRepo},
			expectedErr: errRuntimeFileMissing,
		},
		{
			name: "ssh_config missing for SSH repo",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := writeRuntimeDir(t, []model.RepoConfig{sshRepo})
				require.NoError(t, os.Remove(filepath.Join(dir, "ssh_config")))
				return dir
			},
			repos:       []model.RepoConfig{sshRepo},
			expectedErr: errRuntimeFileMissing,
		},
		{
			name: "known_hosts missing for SSH repo",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := writeRuntimeDir(t, []model.RepoConfig{sshRepo})
				require.NoError(t, os.Remove(filepath.Join(dir, "known_hosts")))
				return dir
			},
			repos:       []model.RepoConfig{sshRepo},
			expectedErr: errRuntimeFileMissing,
		},
		{
			name: "verifyCommit repo missing allowed-signers file",
			setup: func(t *testing.T) string {
				t.Helper()
				return writeRuntimeDir(t, []model.RepoConfig{sshRepo}) // no VerifyCommit, so no allowed-signers written
			},
			repos:       []model.RepoConfig{verifyRepo},
			expectedErr: errMissingAllowedSigners,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runtimeDir := tc.setup(t)
			err := validateSSHRuntime(runtimeDir, tc.repos)

			if tc.expectedErr != nil {
				require.ErrorIs(t, err, tc.expectedErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestLoadConfigWithRuntimeDir(t *testing.T) {
	validRepo := model.RepoConfig{Name: "ha-config", URL: "git@github.com:org/ha-config.git"}

	testCases := []struct {
		name        string
		repos       []model.RepoConfig
		runtimeDir  func(t *testing.T, repos []model.RepoConfig) string
		expectedErr error
	}{
		{
			name:  "valid runtimeDir is accepted",
			repos: []model.RepoConfig{validRepo},
			runtimeDir: func(t *testing.T, repos []model.RepoConfig) string {
				return writeRuntimeDir(t, repos)
			},
		},
		{
			name:  "runtimeDir set but directory missing fails at load",
			repos: []model.RepoConfig{validRepo},
			runtimeDir: func(t *testing.T, _ []model.RepoConfig) string {
				t.Helper()
				return filepath.Join(t.TempDir(), "nonexistent")
			},
			expectedErr: errRuntimeDirMissing,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runtimeDir := tc.runtimeDir(t, tc.repos)
			configPath := writeConfigFile(t, map[string]any{
				"runtimeDir":   runtimeDir,
				"workspaceDir": "/config/.gitops",
				"repos":        tc.repos,
			})
			t.Setenv("GITOPS_CONFIG_FILE", configPath)

			cfg, err := LoadConfig()

			if tc.expectedErr != nil {
				require.ErrorIs(t, err, tc.expectedErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, runtimeDir, cfg.RuntimeDir)
		})
	}
}
