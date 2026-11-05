package tree

import (
	"strings"
	"testing"

	"github.com/lvdousha26/minigit/index"
	"github.com/lvdousha26/minigit/object"
)

func entries(t *testing.T, store *object.Store, paths map[string]string) []index.Entry {
	t.Helper()
	var out []index.Entry
	for p, content := range paths {
		oid, err := store.Write(object.Blob, []byte(content))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, index.Entry{
			Mode: index.ModeFile,
			Size: uint32(len(content)),
			OID:  oid,
			Path: p,
		})
	}
	return out
}

func TestDirectorySortingMatchesGit(t *testing.T) {
	// '.' (0x2e), '-' (0x2d) and the '/' (0x2f) implicitly appended to a
	// directory name put these four entries in a non-obvious order. Getting
	// this wrong makes git fsck --strict reject the tree.
	store := object.NewStore(t.TempDir())
	es := entries(t, store, map[string]string{
		"a/inner.txt": "x",
		"a.txt":       "dot",
		"a-b.txt":     "dash",
		"z.txt":       "z",
	})
	root, err := WriteTree(store, es)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := store.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Type != object.Tree {
		t.Fatalf("root is a %s", obj.Type)
	}
	items, err := Parse(obj.Data)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, it := range items {
		names = append(names, it.Name)
	}
	want := "a-b.txt,a.txt,a,z.txt"
	if got := strings.Join(names, ","); got != want {
		t.Fatalf("tree order = %s, want %s", got, want)
	}
	if items[2].Mode != "40000" {
		t.Fatalf("directory mode = %q, want 40000", items[2].Mode)
	}
}

func TestFlattenAndList(t *testing.T) {
	store := object.NewStore(t.TempDir())
	es := entries(t, store, map[string]string{
		"a/inner.txt": "x",
		"a.txt":       "dot",
		"top":         "t",
	})
	root, err := WriteTree(store, es)
	if err != nil {
		t.Fatal(err)
	}
	flat, err := Flatten(store, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(flat) != 3 {
		t.Fatalf("Flatten returned %d paths: %v", len(flat), flat)
	}
	if flat["a/inner.txt"] != object.Hash(object.Blob, []byte("x")) {
		t.Fatal("Flatten returned the wrong blob id")
	}
	lines, err := List(store, root, true)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{"a/inner.txt", "a.txt", "top"} {
		if !strings.Contains(got, "\t"+want) {
			t.Fatalf("recursive listing is missing %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, " tree ") {
		t.Fatal("recursive listing should not contain directory lines")
	}
	short, err := List(store, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(short, "\n"), " tree ") {
		t.Fatal("non-recursive listing should include the directory line")
	}
}

func TestWriteTreeRejectsFileDirectoryConflict(t *testing.T) {
	store := object.NewStore(t.TempDir())
	es := entries(t, store, map[string]string{"a": "x", "a/b": "y"})
	if _, err := WriteTree(store, es); err == nil {
		t.Fatal("expected a conflict error")
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	for _, data := range [][]byte{
		[]byte("100644"),
		[]byte("100644 name\x00short"),
	} {
		if _, err := Parse(data); err == nil {
			t.Fatalf("expected an error for %q", data)
		}
	}
}
