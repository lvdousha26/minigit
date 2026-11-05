// Package tree turns the flat index into the nested tree objects that a
// commit points at, and reads tree objects back.
package tree

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/lvdousha26/minigit/index"
	"github.com/lvdousha26/minigit/object"
)

type Item struct {
	Mode string // "100644", "100755" or "40000"
	Name string
	OID  string
}

// Parse splits a tree object body into its entries. Each entry is
// <octal mode> <name>\x00<20 raw oid bytes>, with no separator afterwards.
func Parse(data []byte) ([]Item, error) {
	var out []Item
	for i := 0; i < len(data); {
		sp := -1
		for j := i; j < len(data); j++ {
			if data[j] == ' ' {
				sp = j
				break
			}
		}
		if sp < 0 {
			return nil, fmt.Errorf("malformed tree entry: no mode separator")
		}
		nul := -1
		for j := sp + 1; j < len(data); j++ {
			if data[j] == 0 {
				nul = j
				break
			}
		}
		if nul < 0 || nul+21 > len(data) {
			return nil, fmt.Errorf("malformed tree entry: truncated name or oid")
		}
		out = append(out, Item{
			Mode: string(data[i:sp]),
			Name: string(data[sp+1 : nul]),
			OID:  hex.EncodeToString(data[nul+1 : nul+21]),
		})
		i = nul + 21
	}
	return out, nil
}

// IsDir reports whether a tree entry mode names a subtree. Git writes
// directory modes as "40000", without the leading zero.
func IsDir(mode string) bool { return mode == "40000" || mode == "040000" }

func isDir(mode string) bool { return IsDir(mode) }

type node struct {
	dirs  map[string]*node
	files map[string]index.Entry
}

func newNode() *node {
	return &node{dirs: make(map[string]*node), files: make(map[string]index.Entry)}
}

func (n *node) insert(path string, e index.Entry) error {
	parts := strings.Split(path, "/")
	cur := n
	for _, p := range parts[:len(parts)-1] {
		if _, ok := cur.files[p]; ok {
			return fmt.Errorf("%q is both a file and a directory", p)
		}
		if cur.dirs[p] == nil {
			cur.dirs[p] = newNode()
		}
		cur = cur.dirs[p]
	}
	leaf := parts[len(parts)-1]
	if cur.dirs[leaf] != nil {
		return fmt.Errorf("%q is both a file and a directory", leaf)
	}
	cur.files[leaf] = e
	return nil
}

type line struct {
	mode    string
	name    string
	oid     string
	sortKey string
}

// WriteTree writes one tree object per directory, bottom up, and returns the
// root tree id. Git does not store empty directories, so neither do we.
func WriteTree(store *object.Store, entries []index.Entry) (string, error) {
	root := newNode()
	for _, e := range entries {
		if err := root.insert(e.Path, e); err != nil {
			return "", err
		}
	}
	return writeNode(store, root)
}

func writeNode(store *object.Store, n *node) (string, error) {
	var lines []line
	for name, e := range n.files {
		mode := fmt.Sprintf("%o", e.Mode)
		if mode == "" {
			mode = "100644"
		}
		lines = append(lines, line{mode: mode, name: name, oid: e.OID, sortKey: name})
	}
	for name, sub := range n.dirs {
		oid, err := writeNode(store, sub)
		if err != nil {
			return "", err
		}
		// A directory compares as if its name had a trailing slash. Without
		// this, "a.txt" and directory "a" come out in the wrong order and
		// git fsck reports the tree as unsorted.
		lines = append(lines, line{mode: "40000", name: name, oid: oid, sortKey: name + "/"})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].sortKey < lines[j].sortKey })

	var body []byte
	for _, l := range lines {
		raw, err := object.DecodeOID(l.oid)
		if err != nil {
			return "", err
		}
		body = append(body, []byte(l.mode+" "+l.name+"\x00")...)
		body = append(body, raw[:]...)
	}
	return store.Write(object.Tree, body)
}

// Flatten expands a tree into path -> blob id, matching the shape of the index.
func Flatten(store *object.Store, treeOID string) (map[string]string, error) {
	out := make(map[string]string)
	if err := flatten(store, treeOID, "", out); err != nil {
		return nil, err
	}
	return out, nil
}

func flatten(store *object.Store, treeOID, prefix string, out map[string]string) error {
	obj, err := store.Read(treeOID)
	if err != nil {
		return err
	}
	if obj.Type != object.Tree {
		return fmt.Errorf("%s is a %s, not a tree", treeOID, obj.Type)
	}
	items, err := Parse(obj.Data)
	if err != nil {
		return fmt.Errorf("tree %s: %w", treeOID, err)
	}
	for _, it := range items {
		path := prefix + it.Name
		if isDir(it.Mode) {
			if err := flatten(store, it.OID, path+"/", out); err != nil {
				return err
			}
		} else {
			out[path] = it.OID
		}
	}
	return nil
}

// List returns lines in the shape of git ls-tree.
func List(store *object.Store, treeOID string, recursive bool) ([]string, error) {
	var out []string
	if err := list(store, treeOID, "", recursive, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func list(store *object.Store, treeOID, prefix string, recursive bool, out *[]string) error {
	obj, err := store.Read(treeOID)
	if err != nil {
		return err
	}
	if obj.Type != object.Tree {
		return fmt.Errorf("%s is a %s, not a tree", treeOID, obj.Type)
	}
	items, err := Parse(obj.Data)
	if err != nil {
		return err
	}
	for _, it := range items {
		path := prefix + it.Name
		if isDir(it.Mode) {
			// git ls-tree -r lists leaves only; directory lines appear when
			// recursion is off (or with -t).
			if recursive {
				if err := list(store, it.OID, path+"/", recursive, out); err != nil {
					return err
				}
			} else {
				*out = append(*out, fmt.Sprintf("%06s tree %s\t%s", it.Mode, it.OID, path))
			}
			continue
		}
		*out = append(*out, fmt.Sprintf("%06s blob %s\t%s", it.Mode, it.OID, path))
	}
	return nil
}
