package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lvdousha26/minigit/object"
)

func TestInitAndOpenFromSubdirectory(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, "dev"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"objects", "refs/heads", "refs/tags", "HEAD", "config"} {
		if _, err := os.Stat(filepath.Join(root, ".git", p)); err != nil {
			t.Fatalf("init did not create .git/%s: %v", p, err)
		}
	}
	nested := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Open(nested)
	if err != nil {
		t.Fatal(err)
	}
	if r.WorkTree != root {
		t.Fatalf("WorkTree = %s, want %s", r.WorkTree, root)
	}
	ref, oid, err := r.ReadHEAD()
	if err != nil {
		t.Fatal(err)
	}
	if ref != "refs/heads/dev" || oid != "" {
		t.Fatalf("unborn HEAD = %q %q", ref, oid)
	}
}

func TestOpenOutsideRepository(t *testing.T) {
	_, err := Open(t.TempDir())
	if err != ErrNotRepository {
		t.Fatalf("expected ErrNotRepository, got %v", err)
	}
}

func TestRefsAndResolve(t *testing.T) {
	root := t.TempDir()
	r, err := Init(root, "main")
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.Objects.Write(object.Blob, []byte("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef("refs/heads/main", a); err != nil {
		t.Fatal(err)
	}
	_, oid, err := r.ReadHEAD()
	if err != nil || oid != a {
		t.Fatalf("HEAD = %q, %v", oid, err)
	}
	if got, err := r.ResolveOID(a[:8]); err != nil || got != a {
		t.Fatalf("ResolveOID = %q, %v", got, err)
	}
	if _, err := r.ResolveOID("zzzz"); err == nil {
		t.Fatal("expected an error for a non-hex prefix")
	}
	if err := r.WriteHEAD(a); err != nil {
		t.Fatal(err)
	}
	ref, detached, err := r.ReadHEAD()
	if err != nil || ref != "" || detached != a {
		t.Fatalf("detached HEAD = %q %q, %v", ref, detached, err)
	}
}

func TestConfig(t *testing.T) {
	r, err := Init(t.TempDir(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Config("core.filemode"); got != "false" {
		t.Fatalf("core.filemode = %q", got)
	}
	if got := r.Config("user.name"); got != "" {
		t.Fatalf("unexpected user.name = %q", got)
	}
}

func TestCommitRoundTrip(t *testing.T) {
	r, err := Init(t.TempDir(), "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_AUTHOR_NAME", "Ada")
	t.Setenv("GIT_AUTHOR_EMAIL", "ada@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Ada")
	t.Setenv("GIT_COMMITTER_EMAIL", "ada@example.com")
	t.Setenv("GIT_AUTHOR_DATE", "@1700000000 +0000")
	t.Setenv("GIT_COMMITTER_DATE", "@1700000000 +0000")

	tree := object.Hash(object.Tree, nil)
	parent := object.Hash(object.Commit, []byte("parent"))
	oid, err := r.WriteCommit(tree, []string{parent}, "subject\n\nbody\n")
	if err != nil {
		t.Fatal(err)
	}
	obj, err := r.Objects.Read(oid)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Type != object.Commit {
		t.Fatalf("object is a %s", obj.Type)
	}
	if !strings.Contains(string(obj.Data), "author Ada <ada@example.com> 1700000000 +0000") {
		t.Fatalf("author line is missing:\n%s", obj.Data)
	}
	c, err := r.ReadCommit(oid)
	if err != nil {
		t.Fatal(err)
	}
	if c.Tree != tree || len(c.Parents) != 1 || c.Parents[0] != parent {
		t.Fatalf("wrong commit header: %+v", c)
	}
	if c.Message != "subject\n\nbody\n" {
		t.Fatalf("message = %q", c.Message)
	}
	if !strings.HasSuffix(string(obj.Data), "body\n\n") && !strings.HasSuffix(string(obj.Data), "body\n") {
		t.Fatal("commit body is not newline terminated")
	}
}

func TestParseCommitNeedsAValidTree(t *testing.T) {
	if _, err := ParseCommit([]byte("author x <y> 1 +0000\n\nmsg\n")); err == nil {
		t.Fatal("expected an error for a commit without a tree")
	}
	if _, err := ParseCommit([]byte("no separator")); err == nil {
		t.Fatal("expected an error for a commit without a message separator")
	}
}
