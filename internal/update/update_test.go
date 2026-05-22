package update

import (
	"errors"
	"testing"
)

// fakeGitRunner records calls and returns configured values.
type fakeGitRunner struct {
	headCommit   string
	remoteHead   string
	dirty        bool

	headCommitErr error
	remoteHeadErr error
	dirtyErr      error

	headCommitCalls   int
	remoteHeadCalls   int
	dirtyCalls        int
}

func (f *fakeGitRunner) HeadCommit() (string, error) {
	f.headCommitCalls++
	if f.headCommitErr != nil {
		return "", f.headCommitErr
	}
	return f.headCommit, nil
}

func (f *fakeGitRunner) RemoteHead(remote, branch string) (string, error) {
	f.remoteHeadCalls++
	if f.remoteHeadErr != nil {
		return "", f.remoteHeadErr
	}
	return f.remoteHead, nil
}

func (f *fakeGitRunner) IsDirty() (bool, error) {
	f.dirtyCalls++
	if f.dirtyErr != nil {
		return false, f.dirtyErr
	}
	return f.dirty, nil
}

// Verify the interface is satisfied.
var _ GitRunner = (*fakeGitRunner)(nil)

func TestFakeRunner_RemoteHeadCalled(t *testing.T) {
	f := &fakeGitRunner{remoteHead: "abc123"}
	got, err := f.RemoteHead("origin", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abc123" {
		t.Errorf("RemoteHead = %q, want %q", got, "abc123")
	}
	if f.remoteHeadCalls != 1 {
		t.Errorf("remoteHeadCalls = %d, want 1", f.remoteHeadCalls)
	}
	if f.headCommitCalls != 0 {
		t.Errorf("headCommitCalls = %d, want 0", f.headCommitCalls)
	}
}

func TestFakeRunner_HeadCommit(t *testing.T) {
	f := &fakeGitRunner{headCommit: "def456"}
	got, err := f.HeadCommit()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "def456" {
		t.Errorf("HeadCommit = %q, want %q", got, "def456")
	}
}

func TestFakeRunner_IsDirty(t *testing.T) {
	f := &fakeGitRunner{dirty: true}
	got, err := f.IsDirty()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("IsDirty should be true")
	}
}

func TestFakeRunner_HeadCommitError(t *testing.T) {
	f := &fakeGitRunner{headCommitErr: errors.New("not a git repo")}
	_, err := f.HeadCommit()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFakeRunner_RemoteHeadError(t *testing.T) {
	f := &fakeGitRunner{remoteHeadErr: errors.New("ls-remote failed")}
	_, err := f.RemoteHead("origin", "main")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFakeRunner_RemoteHeadEmptyResult(t *testing.T) {
	f := &fakeGitRunner{remoteHead: ""}
	// Empty result should return empty string (caller interprets as "no remote commits")
	got, err := f.RemoteHead("origin", "nonexistent")
	if err != nil {
		t.Fatalf("fake should not error on empty: %v", err)
	}
	if got != "" {
		t.Errorf("RemoteHead with empty result should return empty, got %q", got)
	}
}

func TestFakeRunner_NoFetch(t *testing.T) {
	// Verify the interface has no Fetch method — this is a compile-time
	// check; the test just documents that GitRunner is read-only.
	// If this file compiles, GitRunner has no Fetch.
	f := &fakeGitRunner{}
	_, _ = f.HeadCommit()
	_, _ = f.RemoteHead("origin", "main")
	_, _ = f.IsDirty()
	// No Fetch call — the interface simply doesn't have one.
}
