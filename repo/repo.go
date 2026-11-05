// Package repo locates and maintains the on-disk repository layout: HEAD,
// refs, config and the object store.
package repo

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lvdousha26/minigit/object"
)

var ErrNotRepository = errors.New("not a minigit repository")

type Repo struct {
	WorkTree string
	GitDir   string
	Objects  *object.Store
}

// Init creates the smallest layout that both minigit and real Git accept.
func Init(workTree, branch string) (*Repo, error) {
	if branch == "" {
		branch = "main"
	}
	gitDir := filepath.Join(workTree, ".git")
	for _, d := range []string{"objects", "refs/heads", "refs/tags"} {
		if err := os.MkdirAll(filepath.Join(gitDir, d), 0o755); err != nil {
			return nil, err
		}
	}
	config := "[core]\n" +
		"\trepositoryformatversion = 0\n" +
		"\tfilemode = false\n" +
		"\tbare = false\n" +
		"\tlogallrefupdates = true\n"
	files := map[string]string{
		"HEAD":        "ref: refs/heads/" + branch + "\n",
		"config":      config,
		"description": "Unnamed repository; edit this file to name the repository.\n",
	}
	for name, body := range files {
		p := filepath.Join(gitDir, name)
		if _, err := os.Stat(p); err == nil {
			continue
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return nil, err
		}
	}
	return &Repo{WorkTree: workTree, GitDir: gitDir, Objects: object.NewStore(gitDir)}, nil
}

// Open walks up from start looking for a .git directory.
func Open(start string) (*Repo, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return nil, err
	}
	for {
		candidate := filepath.Join(abs, ".git")
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			return &Repo{WorkTree: abs, GitDir: candidate, Objects: object.NewStore(candidate)}, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return nil, ErrNotRepository
		}
		abs = parent
	}
}

func OpenHere() (*Repo, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return Open(cwd)
}

func (r *Repo) IndexPath() string { return filepath.Join(r.GitDir, "index") }

func (r *Repo) ReadHEAD() (ref string, oid string, err error) {
	data, err := os.ReadFile(filepath.Join(r.GitDir, "HEAD"))
	if err != nil {
		return "", "", err
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "ref: ") {
		if len(line) == 40 {
			return "", line, nil // detached HEAD
		}
		return "", "", fmt.Errorf("bad HEAD: %q", line)
	}
	ref = strings.TrimSpace(strings.TrimPrefix(line, "ref: "))
	body, err := os.ReadFile(filepath.Join(r.GitDir, filepath.FromSlash(ref)))
	if err != nil {
		if os.IsNotExist(err) {
			return ref, "", nil // unborn branch
		}
		return ref, "", err
	}
	return ref, strings.TrimSpace(string(body)), nil
}

func (r *Repo) WriteHEAD(oid string) error {
	return os.WriteFile(filepath.Join(r.GitDir, "HEAD"), []byte(oid+"\n"), 0o644)
}

func (r *Repo) UpdateRef(ref, oid string) error {
	p := filepath.Join(r.GitDir, filepath.FromSlash(ref))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(oid+"\n"), 0o644)
}

// ResolveOID accepts an abbreviated id, as long as it is unambiguous.
func (r *Repo) ResolveOID(prefix string) (string, error) {
	prefix = strings.ToLower(prefix)
	if len(prefix) == 40 {
		if _, err := object.DecodeOID(prefix); err != nil {
			return "", err
		}
		return prefix, nil
	}
	if len(prefix) < 4 {
		return "", fmt.Errorf("object id prefix too short: %q", prefix)
	}
	if _, err := object.DecodeOID(prefix + strings.Repeat("0", 40-len(prefix))); err != nil {
		return "", fmt.Errorf("bad object id %q", prefix)
	}
	dir := filepath.Join(r.Objects.Root, prefix[:2])
	names, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("no object starts with %s", prefix)
	}
	var found []string
	for _, n := range names {
		full := prefix[:2] + n.Name()
		if strings.HasPrefix(full, prefix) {
			found = append(found, full)
		}
	}
	if len(found) == 0 {
		return "", fmt.Errorf("no object starts with %s", prefix)
	}
	if len(found) > 1 {
		return "", fmt.Errorf("ambiguous object id %s: %v", prefix, found)
	}
	return found[0], nil
}

// Config returns a value from .git/config using a deliberately small INI
// reader. Only "section.key = value" lines are needed here.
func (r *Repo) Config(key string) string {
	f, err := os.Open(filepath.Join(r.GitDir, "config"))
	if err != nil {
		return ""
	}
	defer f.Close()
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]") + "."
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			if section+strings.TrimSpace(k) == key {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}
