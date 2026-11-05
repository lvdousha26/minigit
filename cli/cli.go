// Package cli implements the command line surface. Keeping it separate from
// main lets the tests drive real commands without spawning a process.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lvdousha26/minigit/object"
	"github.com/lvdousha26/minigit/repo"
)

const usage = `usage: minigit <command> [args]

  init [path]                       create an empty repository
  hash-object [-w] [-t type] file   print (and optionally store) an object id
  cat-file (-t|-s|-p) <object>      inspect an object
  add <path>...                     stage files
  write-tree                        write the index as tree objects
  commit-tree <tree> -p <parent> -m <msg>
  commit -m <msg>                   record the index
  log [-n <count>]                  walk the first-parent history
  status                            short status
  ls-tree [-r] [<tree-ish>]         list a tree
  rev-parse <rev>                   resolve HEAD, a ref or an object id
`

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	var err error
	switch args[0] {
	case "init":
		err = cmdInit(args[1:], stdout)
	case "hash-object":
		err = cmdHashObject(args[1:], stdout)
	case "cat-file":
		err = cmdCatFile(args[1:], stdout)
	case "add":
		err = cmdAdd(args[1:], stdout)
	case "write-tree":
		err = cmdWriteTree(args[1:], stdout)
	case "commit-tree":
		err = cmdCommitTree(args[1:], stdout)
	case "commit":
		err = cmdCommit(args[1:], stdout)
	case "log":
		err = cmdLog(args[1:], stdout)
	case "status":
		err = cmdStatus(args[1:], stdout)
	case "ls-tree":
		err = cmdLsTree(args[1:], stdout)
	case "rev-parse":
		err = cmdRevParse(args[1:], stdout)
	default:
		fmt.Fprintf(stderr, "minigit: unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, usage)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "minigit: %v\n", err)
		return 1
	}
	return 0
}

func cmdInit(args []string, out io.Writer) error {
	dir := "."
	branch := "main"
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-b":
			i++
			if i >= len(args) {
				return fmt.Errorf("-b needs a branch name")
			}
			branch = args[i]
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag %s", args[i])
		default:
			dir = args[i]
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	if _, err := repo.Init(abs, branch); err != nil {
		return err
	}
	fmt.Fprintf(out, "Initialized empty minigit repository in %s\n", filepath.Join(abs, ".git"))
	return nil
}

func cmdHashObject(args []string, out io.Writer) error {
	write := false
	typ := string(object.Blob)
	var files []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-w":
			write = true
		case args[i] == "-t":
			i++
			if i >= len(args) {
				return fmt.Errorf("-t needs a type")
			}
			typ = args[i]
		case args[i] == "--stdin":
			return fmt.Errorf("--stdin is not supported; pass a file")
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag %s", args[i])
		default:
			files = append(files, args[i])
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("hash-object needs at least one file")
	}
	t := object.Type(typ)
	if !typValid(t) {
		return fmt.Errorf("unknown object type %q", typ)
	}
	var r *repo.Repo
	var err error
	if write {
		r, err = repo.OpenHere()
		if err != nil {
			return err
		}
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		oid := object.Hash(t, data)
		if write {
			oid, err = r.Objects.Write(t, data)
			if err != nil {
				return err
			}
		}
		fmt.Fprintln(out, oid)
	}
	return nil
}

func typValid(t object.Type) bool {
	switch t {
	case object.Blob, object.Tree, object.Commit, object.Tag:
		return true
	}
	return false
}

func cmdCatFile(args []string, out io.Writer) error {
	mode := ""
	var target string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-t", "-s", "-p":
			mode = args[i]
		case "-h", "--help":
			fmt.Fprint(out, "usage: minigit cat-file (-t|-s|-p) <object>\n")
			return nil
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag %s", args[i])
			}
			target = args[i]
		}
	}
	if mode == "" || target == "" {
		return fmt.Errorf("usage: minigit cat-file (-t|-s|-p) <object>")
	}
	r, err := repo.OpenHere()
	if err != nil {
		return err
	}
	oid, err := resolve(r, target)
	if err != nil {
		return err
	}
	obj, err := r.Objects.Read(oid)
	if err != nil {
		return err
	}
	switch mode {
	case "-t":
		fmt.Fprintln(out, obj.Type)
	case "-s":
		fmt.Fprintln(out, len(obj.Data))
	case "-p":
		if obj.Type == object.Tree {
			return printTree(r, oid, out)
		}
		_, err = out.Write(obj.Data)
	}
	return err
}

func cmdRevParse(args []string, out io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: minigit rev-parse <rev>")
	}
	r, err := repo.OpenHere()
	if err != nil {
		return err
	}
	oid, err := resolve(r, args[0])
	if err != nil {
		return err
	}
	fmt.Fprintln(out, oid)
	return nil
}
