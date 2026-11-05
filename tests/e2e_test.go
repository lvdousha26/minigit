// Package tests drives the real CLI and checks the result with the real Git
// binary. The point of this project is compatibility with Git's on-disk
// format, so a self-consistent but unreadable repository would be worthless.
package tests

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lvdousha26/minigit/cli"
)

type harness struct {
	t   *testing.T
	dir string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH; interop tests need the real binary")
	}
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	t.Setenv("GIT_AUTHOR_NAME", "minigit test")
	t.Setenv("GIT_AUTHOR_EMAIL", "minigit@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "minigit test")
	t.Setenv("GIT_COMMITTER_EMAIL", "minigit@example.com")
	t.Setenv("GIT_AUTHOR_DATE", "@1700000000 +0000")
	t.Setenv("GIT_COMMITTER_DATE", "@1700000000 +0000")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return &harness{t: t, dir: dir}
}

func (h *harness) mg(args ...string) string {
	h.t.Helper()
	var out, errs bytes.Buffer
	if code := cli.Run(args, &out, &errs); code != 0 {
		h.t.Fatalf("minigit %v failed (exit %d): %s", args, code, errs.String())
	}
	return out.String()
}

func (h *harness) mgFail(args ...string) string {
	h.t.Helper()
	var out, errs bytes.Buffer
	if code := cli.Run(args, &out, &errs); code == 0 {
		h.t.Fatalf("minigit %v unexpectedly succeeded: %s", args, out.String())
	}
	return errs.String()
}

func (h *harness) git(args ...string) string {
	h.t.Helper()
	full := append([]string{"-c", "core.autocrlf=false", "-c", "core.filemode=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = h.dir
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		h.t.Fatalf("git %v failed: %v\n%s\n%s", args, err, out.String(), errs.String())
	}
	return strings.TrimSpace(out.String())
}

func (h *harness) write(rel, content string) {
	h.t.Helper()
	p := filepath.Join(h.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func TestMinigitRepositoryIsReadableByGit(t *testing.T) {
	h := newHarness(t)
	h.mg("init")
	h.write("a.txt", "hello\n")
	h.write("src/main.go", "package main\n")
	h.mg("add", ".")
	h.mg("commit", "-m", "first commit")

	cat := h.git("cat-file", "-p", "HEAD")
	for _, want := range []string{
		"tree ", "author minigit test <minigit@example.com> 1700000000 +0000", "first commit",
	} {
		if !strings.Contains(cat, want) {
			t.Fatalf("git cat-file -p HEAD is missing %q:\n%s", want, cat)
		}
	}
	if got := h.git("status", "--porcelain"); got != "" {
		t.Fatalf("git thinks the work tree is dirty:\n%s", got)
	}
	if got := h.git("log", "--oneline"); !strings.Contains(got, "first commit") {
		t.Fatalf("git log = %q", got)
	}
	if got := h.git("ls-tree", "-r", "HEAD"); !strings.Contains(got, "ce013625030ba8dba906f756967f9e9ca394464a\ta.txt") {
		t.Fatalf("git ls-tree -r HEAD = %q", got)
	}
	if got := h.git("fsck", "--strict"); got != "" {
		t.Fatalf("git fsck --strict complained: %s", got)
	}
	if got := h.git("rev-parse", "HEAD"); got == "" || len(got) != 40 {
		t.Fatalf("git rev-parse HEAD = %q", got)
	}
}

func TestTreeSortingSurvivesGitFsck(t *testing.T) {
	h := newHarness(t)
	h.mg("init")
	h.write("a/inner.txt", "x\n")
	h.write("a.txt", "dot\n")
	h.write("a-b.txt", "dash\n")
	h.write("z.txt", "z\n")
	h.mg("add", ".")
	h.mg("commit", "-m", "sorting trap")

	if got := h.git("fsck", "--strict"); got != "" {
		t.Fatalf("git fsck --strict complained about tree order: %s", got)
	}
	want := h.git("ls-tree", "-r", "HEAD")
	got := strings.TrimSpace(h.mg("ls-tree", "-r"))
	if got != want {
		t.Fatalf("minigit and git disagree on the tree:\nminigit:\n%s\ngit:\n%s", got, want)
	}
}

func TestMinigitReadsRepositoryCreatedByGit(t *testing.T) {
	h := newHarness(t)
	h.git("init", "-q")
	h.write("from-git.txt", "written by git\n")
	h.git("add", "from-git.txt")
	h.git("commit", "-q", "-m", "made by real git")

	head := h.git("rev-parse", "HEAD")
	if got := strings.TrimSpace(h.mg("cat-file", "-p", "HEAD")); !strings.Contains(got, "made by real git") || !strings.Contains(got, "tree ") {
		t.Fatalf("minigit cat-file -p HEAD = %q", got)
	}
	if got := h.mg("log"); !strings.Contains(got, head) || !strings.Contains(got, "made by real git") {
		t.Fatalf("minigit log = %q", got)
	}
	if got := strings.TrimSpace(h.mg("status")); got != "" {
		t.Fatalf("minigit thinks git's clean tree is dirty:\n%s", got)
	}
	if got := strings.TrimSpace(h.mg("rev-parse", "HEAD")); got != head {
		t.Fatalf("minigit rev-parse = %q, want %q", got, head)
	}
	// The index written by git must produce the same tree id.
	if got, want := strings.TrimSpace(h.mg("write-tree")), h.git("write-tree"); got != want {
		t.Fatalf("write-tree = %q, git says %q", got, want)
	}
	if got := h.mg("ls-tree", "-r"); !strings.Contains(got, "from-git.txt") {
		t.Fatalf("minigit ls-tree -r = %q", got)
	}
}

func TestTwoCommitsAndStatusTransitions(t *testing.T) {
	h := newHarness(t)
	h.mg("init")
	h.write("a.txt", "one\n")
	h.write("gone.txt", "bye\n")
	h.mg("add", ".")
	h.mg("commit", "-m", "first")

	if got := h.mg("status"); strings.TrimSpace(got) != "" {
		t.Fatalf("status after commit is not clean: %q", got)
	}
	h.write("a.txt", "two\n")
	h.write("untracked.txt", "?\n")
	if err := os.Remove(filepath.Join(h.dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	status := h.mg("status")
	if !strings.Contains(status, " M a.txt") || !strings.Contains(status, "?? untracked.txt") {
		t.Fatalf("unexpected status:\n%s", status)
	}
	h.mg("add", "a.txt")
	status = h.mg("status")
	if !strings.Contains(status, "M  a.txt") {
		t.Fatalf("staged change is missing:\n%s", status)
	}
	h.write("a.txt", "three\n")
	status = h.mg("status")
	if !strings.Contains(status, "MM a.txt") {
		t.Fatalf("expected both columns set for a staged-then-edited file:\n%s", status)
	}
	h.mg("add", "a.txt")
	h.mg("add", "gone.txt") // stage a deletion via a missing path
	h.mg("commit", "-m", "second")

	log := h.mg("log")
	if !strings.Contains(log, "second") || !strings.Contains(log, "first") {
		t.Fatalf("log is missing a commit:\n%s", log)
	}
	if got := h.git("log", "--oneline"); len(strings.Split(got, "\n")) != 2 {
		t.Fatalf("git sees the wrong number of commits: %q", got)
	}
	if got := h.git("show", "--stat", "--oneline", "HEAD"); !strings.Contains(got, "a.txt") || !strings.Contains(got, "gone.txt") {
		t.Fatalf("git show does not reflect the edit and deletion:\n%s", got)
	}
	if got := h.git("status", "--porcelain"); got != "?? untracked.txt" {
		t.Fatalf("git status = %q, want only the untracked file", got)
	}
}

func TestContentAddressingAndAbbreviatedIDs(t *testing.T) {
	h := newHarness(t)
	h.mg("init")
	h.write("left.txt", "same content\n")
	h.write("nested/right.txt", "same content\n")
	h.mg("add", ".")

	tree := h.git("ls-tree", "-r", strings.TrimSpace(h.mg("write-tree")))
	// Both paths must point at the same blob object.
	first := strings.Fields(strings.Split(tree, "\n")[0])[2]
	second := strings.Fields(strings.Split(tree, "\n")[1])[2]
	if first != second {
		t.Fatalf("identical content got two object ids: %s vs %s", first, second)
	}
	if got := h.mg("cat-file", "-p", first[:8]); got != "same content\n" {
		t.Fatalf("cat-file with an abbreviated id = %q", got)
	}
	if got := strings.TrimSpace(h.mg("cat-file", "-t", first)); got != "blob" {
		t.Fatalf("cat-file -t = %q", got)
	}
	if got := strings.TrimSpace(h.mg("cat-file", "-s", first)); got != fmt.Sprint(len("same content\n")) {
		t.Fatalf("cat-file -s = %q", got)
	}
	if got := h.mgFail("rev-parse", "HEAD"); !strings.Contains(got, "does not point at a commit") {
		t.Fatalf("expected the unborn-HEAD message, got %q", got)
	}
}

func TestErrorsAndCorruption(t *testing.T) {
	h := newHarness(t)
	if got := h.mgFail("add", "a.txt"); !strings.Contains(got, "not a minigit repository") {
		t.Fatalf("unexpected error outside a repository: %q", got)
	}
	h.mg("init")
	h.write("a.txt", "data\n")
	h.mg("add", "a.txt")
	if got := h.mgFail("cat-file", "-p", "deadbeef"); got == "" {
		t.Fatal("expected an error for a missing object")
	}
	if got := h.mgFail("commit"); !strings.Contains(got, "-m") {
		t.Fatalf("unexpected error for a missing message: %q", got)
	}
	// Corrupt one loose object and make sure reads refuse it.
	oid := strings.TrimSpace(h.mg("hash-object", "a.txt"))
	p := filepath.Join(h.dir, ".git", "objects", oid[:2], oid[2:])
	// Loose objects are stored read-only, like in a real Git repository.
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("not a zlib stream"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := h.mgFail("cat-file", "-p", oid); !strings.Contains(got, "corrupt") {
		t.Fatalf("expected a corruption error, got %q", got)
	}
}
