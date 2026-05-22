package update

import (
	"fmt"
	"os/exec"
	"strings"
)

// GitRunner abstracts read-only git operations for update checking.
// All methods are side-effect-free with respect to the working copy
// and remote tracking refs. No fetch, pull, merge, or push.
// A real implementation uses os/exec; tests can inject a fake.
type GitRunner interface {
	// HeadCommit returns the current HEAD commit hash (git rev-parse HEAD).
	HeadCommit() (string, error)
	// RemoteHead returns the remote branch tip commit hash (git ls-remote).
	// Does NOT write remote tracking refs. Returns error if ls-remote output is empty.
	RemoteHead(remote, branch string) (string, error)
	// IsDirty returns true when the worktree has uncommitted changes.
	IsDirty() (bool, error)
}

// RealGitRunner runs actual git commands via os/exec.
type RealGitRunner struct {
	RepoDir string
}

// NewRealGitRunner creates a git runner for the given repo directory.
func NewRealGitRunner(repoDir string) *RealGitRunner {
	return &RealGitRunner{RepoDir: repoDir}
}

func (r *RealGitRunner) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.RepoDir
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		return "", fmt.Errorf("git %s: %w%s", strings.Join(args, " "), err, stderr)
	}
	return strings.TrimSpace(string(out)), nil
}

// HeadCommit returns the current HEAD commit hash.
func (r *RealGitRunner) HeadCommit() (string, error) {
	return r.git("rev-parse", "--verify", "HEAD")
}

// RemoteHead uses git ls-remote to get the remote branch tip commit.
// This is side-effect-free: it does NOT write .git/refs/remotes/ or FETCH_HEAD.
// It parses the first column (commit hash) from the ls-remote output.
func (r *RealGitRunner) RemoteHead(remote, branch string) (string, error) {
	ref := "refs/heads/" + branch
	out, err := r.git("ls-remote", remote, ref)
	if err != nil {
		return "", fmt.Errorf("ls-remote %s %s: %w", remote, ref, err)
	}
	if out == "" {
		return "", fmt.Errorf("ls-remote %s %s: no output — remote branch may not exist or remote is unreachable", remote, ref)
	}
	// ls-remote output: "<commit-hash>\t<refname>"
	fields := strings.Fields(out)
	if len(fields) < 1 {
		return "", fmt.Errorf("ls-remote %s %s: unexpected output: %q", remote, ref, out)
	}
	return fields[0], nil
}

// IsDirty returns true when the worktree has uncommitted changes.
func (r *RealGitRunner) IsDirty() (bool, error) {
	out, err := r.git("status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}
