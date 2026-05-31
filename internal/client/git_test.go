package client

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	gitCmd     = "git"
	branchMain = "main"
)

func mustRun(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), gitCmd, args...) //nolint:gosec
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s %v: %s", gitCmd, args, out)
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.Mkdir(dir, 0750))
	mustRun(t, "init", "-b", branchMain, dir)
	mustRun(t, "-C", dir, "config", "user.email", "test@test.com")
	mustRun(t, "-C", dir, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0600))
	mustRun(t, "-C", dir, "add", ".")
	mustRun(t, "-C", dir, "commit", "-m", "init")

	return dir
}

func TestShellGitClient_BareClone(t *testing.T) {
	t.Parallel()
	gc := ShellGitClient{}

	testCases := []struct {
		name        string
		setup       func(t *testing.T) string
		expectedErr string
	}{
		{
			name:  "happy path: clones repo into bare dir",
			setup: initGitRepo,
		},
		{
			name: "error path: invalid URL",
			setup: func(t *testing.T) string {
				t.Helper()

				return "/definitely/not/a/repo"
			},
			expectedErr: gitCmd,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			url := tc.setup(t)
			bareDir := filepath.Join(t.TempDir(), "bare")
			err := gc.BareClone(context.Background(), url, bareDir)
			if tc.expectedErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestShellGitClient_Fetch(t *testing.T) {
	t.Parallel()
	gc := ShellGitClient{}

	testCases := []struct {
		name        string
		setup       func(t *testing.T, bareDir string)
		expectedErr string
	}{
		{
			name: "happy path: fetches from origin",
			setup: func(t *testing.T, bareDir string) {
				t.Helper()
				require.NoError(t, gc.BareClone(context.Background(), initGitRepo(t), bareDir))
			},
		},
		{
			name:        "error path: nonexistent dir",
			setup:       func(t *testing.T, bareDir string) { t.Helper() },
			expectedErr: gitCmd,
		},
		{
			name: "error path: context cancelled",
			setup: func(t *testing.T, bareDir string) {
				t.Helper()
				require.NoError(t, gc.BareClone(context.Background(), initGitRepo(t), bareDir))
			},
			expectedErr: gitCmd,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bareDir := filepath.Join(t.TempDir(), "bare")
			tc.setup(t, bareDir)

			ctx := context.Background()
			if tc.name == "error path: context cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			err := gc.Fetch(ctx, bareDir)
			if tc.expectedErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestShellGitClient_WorktreePrune(t *testing.T) {
	t.Parallel()
	gc := ShellGitClient{}

	testCases := []struct {
		name        string
		setup       func(t *testing.T, bareDir string)
		expectedErr string
	}{
		{
			name: "happy path: prunes stale worktree entries",
			setup: func(t *testing.T, bareDir string) {
				t.Helper()
				require.NoError(t, gc.BareClone(context.Background(), initGitRepo(t), bareDir))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bareDir := filepath.Join(t.TempDir(), "bare")
			tc.setup(t, bareDir)
			err := gc.WorktreePrune(context.Background(), bareDir)
			if tc.expectedErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestShellGitClient_WorktreeAdd(t *testing.T) {
	t.Parallel()
	gc := ShellGitClient{}

	testCases := []struct {
		name        string
		ref         string
		expectedErr string
	}{
		{
			name: "happy path: adds worktree at ref",
			ref:  branchMain,
		},
		{
			name:        "error path: ref does not exist",
			ref:         "no-such-branch",
			expectedErr: gitCmd,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmpDir := t.TempDir()
			bareDir := filepath.Join(tmpDir, "bare")
			wtDir := filepath.Join(tmpDir, "worktree")
			require.NoError(t, gc.BareClone(context.Background(), initGitRepo(t), bareDir))

			err := gc.WorktreeAdd(context.Background(), bareDir, wtDir, tc.ref)
			if tc.expectedErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestShellGitClient_VerifyCommit(t *testing.T) {
	t.Parallel()
	gc := ShellGitClient{}

	testCases := []struct {
		name        string
		expectedErr string
	}{
		{
			name:        "error path: fails without GPG key",
			expectedErr: gitCmd,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmpDir := t.TempDir()
			bareDir := filepath.Join(tmpDir, "bare")
			wtDir := filepath.Join(tmpDir, "worktree")
			require.NoError(t, gc.BareClone(context.Background(), initGitRepo(t), bareDir))
			require.NoError(t, gc.WorktreeAdd(context.Background(), bareDir, wtDir, branchMain))

			err := gc.VerifyCommit(context.Background(), wtDir)
			if tc.expectedErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
