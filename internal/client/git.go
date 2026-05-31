package client

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

type GitClient interface {
	BareClone(ctx context.Context, url, dest string) error
	Fetch(ctx context.Context, bareDir string) error
	WorktreePrune(ctx context.Context, bareDir string) error
	WorktreeAdd(ctx context.Context, bareDir, dest, ref string) error
	VerifyCommit(ctx context.Context, worktreeDir string) error
}

type ShellGitClient struct{}

func (g ShellGitClient) BareClone(ctx context.Context, url, dest string) error {
	return runGit(ctx, "clone", "--bare", url, dest)
}

func (g ShellGitClient) Fetch(ctx context.Context, bareDir string) error {
	return runGit(ctx, "-C", bareDir, "fetch", "--all", "--prune")
}

func (g ShellGitClient) WorktreePrune(ctx context.Context, bareDir string) error {
	return runGit(ctx, "-C", bareDir, "worktree", "prune")
}

func (g ShellGitClient) WorktreeAdd(ctx context.Context, bareDir, dest, ref string) error {
	return runGit(ctx, "-C", bareDir, "worktree", "add", "--force", dest, ref)
}

func (g ShellGitClient) VerifyCommit(ctx context.Context, worktreeDir string) error {
	return runGit(ctx, "-C", worktreeDir, "verify-commit", "HEAD")
}

func runGit(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("git %v: %w; output: %s", args, err, buf.String())
	}

	return nil
}
