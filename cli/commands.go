package cli

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lvdousha26/minigit/index"
	"github.com/lvdousha26/minigit/object"
	"github.com/lvdousha26/minigit/repo"
	"github.com/lvdousha26/minigit/tree"
)

func cmdAdd(args []string, _ io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: minigit add <path>...")
	}
	r, err := repo.OpenHere()
	if err != nil {
		return err
	}
	ix, err := readIndex(r)
	if err != nil {
		return err
	}
	for _, arg := range args {
		rel, err := relPath(r, arg)
		if err != nil {
			return err
		}
		abs := filepath.Join(r.WorkTree, filepath.FromSlash(rel))
		fi, err := os.Stat(abs)
		if os.IsNotExist(err) {
			// "minigit add deleted.txt" stages the removal, like git add -u.
			out := ix.Entries[:0]
			for _, e := range ix.Entries {
				if e.Path == rel || (rel != "" && strings.HasPrefix(e.Path, rel+"/")) {
					continue
				}
				out = append(out, e)
			}
			ix.Entries = out
			continue
		}
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			if err := addFile(r, ix, rel, abs, fi); err != nil {
				return err
			}
			continue
		}
		if err := filepath.WalkDir(abs, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if d.Name() == ".git" && p != abs {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			sub, err := relPath(r, p)
			if err != nil {
				return err
			}
			return addFile(r, ix, sub, p, info)
		}); err != nil {
			return err
		}
	}
	return ix.Write(r.IndexPath())
}

func addFile(r *repo.Repo, ix *index.Index, rel, abs string, fi os.FileInfo) error {
	if rel == "" {
		return fmt.Errorf("cannot add a file without a path")
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	oid, err := r.Objects.Write(object.Blob, data)
	if err != nil {
		return err
	}
	mode := uint32(index.ModeFile)
	if fi.Mode().Perm()&0o111 != 0 {
		mode = index.ModeExec
	}
	ix.Upsert(index.Entry{
		CtimeSec: uint32(fi.ModTime().Unix()),
		MtimeSec: uint32(fi.ModTime().Unix()),
		Mode:     mode,
		Size:     uint32(len(data)),
		OID:      oid,
		Path:     rel,
	})
	return nil
}

func cmdWriteTree(args []string, out io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("write-tree takes no arguments")
	}
	r, err := repo.OpenHere()
	if err != nil {
		return err
	}
	ix, err := readIndex(r)
	if err != nil {
		return err
	}
	oid, err := tree.WriteTree(r.Objects, ix.Entries)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, oid)
	return nil
}

func cmdCommitTree(args []string, out io.Writer) error {
	var positional []string
	var parents, messages []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-p":
			i++
			if i >= len(args) {
				return fmt.Errorf("-p needs an object id")
			}
			parents = append(parents, args[i])
		case "-m":
			i++
			if i >= len(args) {
				return fmt.Errorf("-m needs a message")
			}
			messages = append(messages, args[i])
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag %s", args[i])
			}
			positional = append(positional, args[i])
		}
	}
	if len(positional) != 1 || len(messages) == 0 {
		return fmt.Errorf("usage: minigit commit-tree <tree> [-p <parent>] -m <msg>")
	}
	r, err := repo.OpenHere()
	if err != nil {
		return err
	}
	treeOID, err := r.ResolveOID(positional[0])
	if err != nil {
		return err
	}
	for i, p := range parents {
		parents[i], err = r.ResolveOID(p)
		if err != nil {
			return err
		}
	}
	oid, err := r.WriteCommit(treeOID, parents, strings.Join(messages, "\n\n"))
	if err != nil {
		return err
	}
	fmt.Fprintln(out, oid)
	return nil
}

func cmdCommit(args []string, out io.Writer) error {
	var messages []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-m":
			i++
			if i >= len(args) {
				return fmt.Errorf("-m needs a message")
			}
			messages = append(messages, args[i])
		default:
			return fmt.Errorf("unknown argument %q (usage: minigit commit -m <msg>)", args[i])
		}
	}
	if len(messages) == 0 {
		return fmt.Errorf("usage: minigit commit -m <msg>")
	}
	r, err := repo.OpenHere()
	if err != nil {
		return err
	}
	ix, err := readIndex(r)
	if err != nil {
		return err
	}
	treeOID, err := tree.WriteTree(r.Objects, ix.Entries)
	if err != nil {
		return err
	}
	ref, parent, err := r.ReadHEAD()
	if err != nil {
		return err
	}
	var parents []string
	if parent != "" {
		prev, err := r.ReadCommit(parent)
		if err != nil {
			return err
		}
		if prev.Tree == treeOID {
			return fmt.Errorf("nothing to commit, index matches HEAD")
		}
		parents = append(parents, parent)
	}
	msg := strings.Join(messages, "\n\n")
	oid, err := r.WriteCommit(treeOID, parents, msg)
	if err != nil {
		return err
	}
	if ref == "" {
		err = r.WriteHEAD(oid)
	} else {
		err = r.UpdateRef(ref, oid)
	}
	if err != nil {
		return err
	}
	branch := strings.TrimPrefix(ref, "refs/heads/")
	if parent == "" {
		fmt.Fprintf(out, "[%s (root-commit) %s] %s\n", branch, oid[:7], strings.SplitN(msg, "\n", 2)[0])
	} else {
		fmt.Fprintf(out, "[%s %s] %s\n", branch, oid[:7], strings.SplitN(msg, "\n", 2)[0])
	}
	return nil
}

func cmdLog(args []string, out io.Writer) error {
	limit := 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-n":
			i++
			if i >= len(args) {
				return fmt.Errorf("-n needs a count")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("bad count %q", args[i])
			}
			limit = n
		default:
			return fmt.Errorf("unknown argument %q", args[i])
		}
	}
	r, err := repo.OpenHere()
	if err != nil {
		return err
	}
	_, oid, err := r.ReadHEAD()
	if err != nil {
		return err
	}
	for n := 0; oid != "" && (limit == 0 || n < limit); n++ {
		c, err := r.ReadCommit(oid)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "commit %s\nAuthor: %s\nDate:   %s\n\n", oid, authorName(c.Author), authorDate(c.Author))
		for _, line := range strings.Split(strings.TrimRight(c.Message, "\n"), "\n") {
			fmt.Fprintf(out, "    %s\n", line)
		}
		fmt.Fprintln(out)
		if len(c.Parents) == 0 {
			break
		}
		oid = c.Parents[0]
	}
	return nil
}

func authorName(a string) string {
	if i := strings.Index(a, " <"); i > 0 {
		return a[:i]
	}
	return a
}

func authorDate(a string) string {
	fields := strings.Fields(a)
	if len(fields) < 2 {
		return a
	}
	ts, err := strconv.ParseInt(fields[len(fields)-2], 10, 64)
	if err != nil {
		return a
	}
	loc := time.Local
	if len(fields) >= 1 && len(fields[len(fields)-1]) == 5 {
		if _, off, err := parseTZ(fields[len(fields)-1]); err == nil {
			loc = time.FixedZone(fields[len(fields)-1], off)
		}
	}
	return time.Unix(ts, 0).In(loc).Format("Mon Jan 2 15:04:05 2006 -0700")
}

func parseTZ(s string) (string, int, error) {
	if len(s) != 5 || (s[0] != '+' && s[0] != '-') {
		return "", 0, fmt.Errorf("bad timezone")
	}
	h, err1 := strconv.Atoi(s[1:3])
	m, err2 := strconv.Atoi(s[3:5])
	if err1 != nil || err2 != nil {
		return "", 0, fmt.Errorf("bad timezone")
	}
	off := h*3600 + m*60
	if s[0] == '-' {
		off = -off
	}
	return s, off, nil
}

func cmdStatus(args []string, out io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("status takes no arguments")
	}
	r, err := repo.OpenHere()
	if err != nil {
		return err
	}
	ix, err := readIndex(r)
	if err != nil {
		return err
	}
	idx := make(map[string]string)
	for _, e := range ix.Entries {
		idx[e.Path] = e.OID
	}
	head := make(map[string]string)
	_, headOID, err := r.ReadHEAD()
	if err != nil {
		return err
	}
	if headOID != "" {
		c, err := r.ReadCommit(headOID)
		if err != nil {
			return err
		}
		head, err = tree.Flatten(r.Objects, c.Tree)
		if err != nil {
			return err
		}
	}
	work := make(map[string]string)
	if err := walkWorkTree(r, func(rel string, _ fs.DirEntry) error {
		data, err := os.ReadFile(filepath.Join(r.WorkTree, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		work[rel] = object.Hash(object.Blob, data)
		return nil
	}); err != nil {
		return err
	}

	all := make(map[string]bool)
	for p := range head {
		all[p] = true
	}
	for p := range idx {
		all[p] = true
	}
	for p := range work {
		all[p] = true
	}
	paths := make([]string, 0, len(all))
	for p := range all {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		h, inHead := head[p]
		i, inIdx := idx[p]
		w, inWork := work[p]
		if !inIdx && !inHead && inWork {
			fmt.Fprintf(out, "?? %s\n", p)
			continue
		}
		// Two columns, like git status --short: index vs HEAD, then work
		// tree vs index. A file can be both staged and edited afterwards.
		x, y := ' ', ' '
		switch {
		case inIdx && !inHead:
			x = 'A'
		case !inIdx && inHead:
			x = 'D'
		case inIdx && inHead && i != h:
			x = 'M'
		}
		switch {
		case inIdx && !inWork:
			y = 'D'
		case inIdx && inWork && i != w:
			y = 'M'
		}
		if x != ' ' || y != ' ' {
			fmt.Fprintf(out, "%c%c %s\n", x, y, p)
		}
	}
	return nil
}

func cmdLsTree(args []string, out io.Writer) error {
	recursive := false
	var target string
	for _, a := range args {
		switch {
		case a == "-r":
			recursive = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %s", a)
		default:
			target = a
		}
	}
	if target == "" {
		target = "HEAD"
	}
	r, err := repo.OpenHere()
	if err != nil {
		return err
	}
	oid, err := treeish(r, target)
	if err != nil {
		return err
	}
	lines, err := tree.List(r.Objects, oid, recursive)
	if err != nil {
		return err
	}
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
	return nil
}
