package cli

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lvdousha26/minigit/index"
	"github.com/lvdousha26/minigit/object"
	"github.com/lvdousha26/minigit/repo"
	"github.com/lvdousha26/minigit/tree"
)

func resolve(r *repo.Repo, s string) (string, error) {
	if s == "HEAD" {
		_, oid, err := r.ReadHEAD()
		if err != nil {
			return "", err
		}
		if oid == "" {
			return "", fmt.Errorf("HEAD does not point at a commit yet")
		}
		return oid, nil
	}
	for _, ref := range []string{s, "refs/heads/" + s, "refs/tags/" + s} {
		p := filepath.Join(r.GitDir, filepath.FromSlash(ref))
		if data, err := os.ReadFile(p); err == nil {
			oid := strings.TrimSpace(string(data))
			if _, err := object.DecodeOID(oid); err == nil {
				return oid, nil
			}
		}
	}
	return r.ResolveOID(s)
}

func treeish(r *repo.Repo, s string) (string, error) {
	oid, err := resolve(r, s)
	if err != nil {
		return "", err
	}
	obj, err := r.Objects.Read(oid)
	if err != nil {
		return "", err
	}
	if obj.Type == object.Tree {
		return oid, nil
	}
	if obj.Type != object.Commit {
		return "", fmt.Errorf("%s is a %s, not a tree or commit", oid, obj.Type)
	}
	c, err := repo.ParseCommit(obj.Data)
	if err != nil {
		return "", err
	}
	return c.Tree, nil
}

func readIndex(r *repo.Repo) (*index.Index, error) {
	return index.Read(r.IndexPath())
}

func printTree(r *repo.Repo, oid string, out io.Writer) error {
	obj, err := r.Objects.Read(oid)
	if err != nil {
		return err
	}
	items, err := tree.Parse(obj.Data)
	if err != nil {
		return err
	}
	for _, it := range items {
		typ := "blob"
		if tree.IsDir(it.Mode) {
			typ = "tree"
		}
		fmt.Fprintf(out, "%06s %s %s\t%s\n", it.Mode, typ, it.OID, it.Name)
	}
	return nil
}

// relPath maps a command line path to a slash separated path relative to the
// work tree, which is the form used in the index and in tree objects.
func relPath(r *repo.Repo, arg string) (string, error) {
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(r.WorkTree, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the work tree", arg)
	}
	if rel == "." {
		return "", nil
	}
	return filepath.ToSlash(rel), nil
}

func walkWorkTree(r *repo.Repo, fn func(rel string, d fs.DirEntry) error) error {
	return filepath.WalkDir(r.WorkTree, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == r.WorkTree {
				return nil
			}
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(r.WorkTree, p)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel), d)
	})
}
