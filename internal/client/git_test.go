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

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s %v: %s", name, args, out)
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.Mkdir(dir, 0755))
	mustRun(t, "git", "init", "-b", "main", dir)
	mustRun(t, "git", "-C", dir, "config", "user.email", "test@test.com")
	mustRun(t, "git", "-C", dir, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0644))
	mustRun(t, "git", "-C", dir, "add", ".")
	mustRun(t, "git", "-C", dir, "commit", "-m", "init")
	return dir
}

func TestShellGitClient_BareClone(t *testing.T) {
	gc := ShellGitClient{}

	testCases := []struct {
		name        string
		setup       func(t *testing.T) string
		expectedErr string
	}{
		{
			name: "happy path: clones repo into bare dir",
			setup: func(t *testing.T) string {
				return initGitRepo(t)
			},
		},
		{
			name: "error path: invalid URL",
			setup: func(t *testing.T) string {
				return "/definitely/not/a/repo"
			},
			expectedErr: "git",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
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
	gc := ShellGitClient{}

	testCases := []struct {
		name        string
		setup       func(t *testing.T, bareDir string)
		expectedErr string
	}{
		{
			name: "happy path: fetches from origin",
			setup: func(t *testing.T, bareDir string) {
				require.NoError(t, gc.BareClone(context.Background(), initGitRepo(t), bareDir))
			},
		},
		{
			name:        "error path: nonexistent dir",
			setup:       func(t *testing.T, _ string) {},
			expectedErr: "git",
		},
		{
			name: "error path: context cancelled",
			setup: func(t *testing.T, bareDir string) {
				require.NoError(t, gc.BareClone(context.Background(), initGitRepo(t), bareDir))
			},
			expectedErr: "git",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
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
	gc := ShellGitClient{}

	testCases := []struct {
		name        string
		setup       func(t *testing.T, bareDir string)
		expectedErr string
	}{
		{
			name: "happy path: prunes stale worktree entries",
			setup: func(t *testing.T, bareDir string) {
				require.NoError(t, gc.BareClone(context.Background(), initGitRepo(t), bareDir))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
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
	gc := ShellGitClient{}

	testCases := []struct {
		name        string
		ref         string
		expectedErr string
	}{
		{
			name: "happy path: adds worktree at ref",
			ref:  "main",
		},
		{
			name:        "error path: ref does not exist",
			ref:         "no-such-branch",
			expectedErr: "git",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
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
	gc := ShellGitClient{}

	testCases := []struct {
		name        string
		expectedErr string
	}{
		{
			name:        "error path: fails without GPG key",
			expectedErr: "git",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			bareDir := filepath.Join(tmpDir, "bare")
			wtDir := filepath.Join(tmpDir, "worktree")
			require.NoError(t, gc.BareClone(context.Background(), initGitRepo(t), bareDir))
			require.NoError(t, gc.WorktreeAdd(context.Background(), bareDir, wtDir, "main"))

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
