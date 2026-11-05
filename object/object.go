// Package object reads and writes loose Git objects.
//
// A loose object is the byte string
//
//	<type> <decimal byte count>\x00<content>
//
// compressed with zlib and stored at .git/objects/<first 2 hex>/<rest>.
// The file name is the SHA-1 of the *uncompressed* byte string, which is why
// two identical blobs can never occupy two different names.
package object

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Type string

const (
	Blob   Type = "blob"
	Tree   Type = "tree"
	Commit Type = "commit"
	Tag    Type = "tag"
)

func (t Type) valid() bool {
	switch t {
	case Blob, Tree, Commit, Tag:
		return true
	}
	return false
}

var ErrNotFound = errors.New("object not found")

type Object struct {
	Type Type
	Data []byte
}

// Serialize is the exact byte sequence that gets hashed and compressed.
func Serialize(t Type, data []byte) []byte {
	head := []byte(fmt.Sprintf("%s %d\x00", t, len(data)))
	return append(head, data...)
}

// Hash returns the object id without writing anything to disk.
func Hash(t Type, data []byte) string {
	sum := sha1.Sum(Serialize(t, data))
	return hex.EncodeToString(sum[:])
}

func DecodeOID(s string) ([20]byte, error) {
	var out [20]byte
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 20 {
		return out, fmt.Errorf("invalid object id %q", s)
	}
	copy(out[:], b)
	return out, nil
}

func OIDString(raw [20]byte) string { return hex.EncodeToString(raw[:]) }

type Store struct {
	Root string // .git/objects
}

func NewStore(gitDir string) *Store {
	return &Store{Root: filepath.Join(gitDir, "objects")}
}

func (s *Store) Path(oid string) string {
	return filepath.Join(s.Root, oid[:2], oid[2:])
}

func (s *Store) Exists(oid string) bool {
	_, err := os.Stat(s.Path(oid))
	return err == nil
}

// Write stores an object and returns its id. Writing the same content twice
// is a no-op: content addressing makes duplicates invisible anyway.
func (s *Store) Write(t Type, data []byte) (string, error) {
	if !t.valid() {
		return "", fmt.Errorf("unknown object type %q", t)
	}
	raw := Serialize(t, data)
	sum := sha1.Sum(raw)
	oid := hex.EncodeToString(sum[:])

	dir := filepath.Join(s.Root, oid[:2])
	final := filepath.Join(dir, oid[2:])
	if _, err := os.Stat(final); err == nil {
		return oid, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	// Write to a sibling temp file first. A reader must never see a half
	// written zlib stream, and rename is atomic on the filesystems we target.
	tmp, err := os.CreateTemp(dir, "tmp_obj_*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	zw := zlib.NewWriter(tmp)
	if _, err := zw.Write(raw); err != nil {
		zw.Close()
		tmp.Close()
		return "", err
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, final); err != nil {
		return "", err
	}
	_ = os.Chmod(final, 0o444)
	return oid, nil
}

// Read loads a loose object. oid must be the full 40-character hex id.
func (s *Store) Read(oid string) (*Object, error) {
	if len(oid) != 40 {
		return nil, fmt.Errorf("invalid object id %q", oid)
	}
	if _, err := DecodeOID(oid); err != nil {
		return nil, err
	}
	f, err := os.Open(s.Path(oid))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, oid)
		}
		return nil, err
	}
	defer f.Close()

	zr, err := zlib.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("corrupt object %s: %w", oid, err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		zr.Close()
		return nil, fmt.Errorf("corrupt object %s: %w", oid, err)
	}
	if err := zr.Close(); err != nil {
		return nil, fmt.Errorf("corrupt object %s: %w", oid, err)
	}
	return parse(oid, raw)
}

func parse(oid string, raw []byte) (*Object, error) {
	nul := bytes.IndexByte(raw, 0)
	if nul < 0 {
		return nil, fmt.Errorf("corrupt object %s: missing header terminator", oid)
	}
	fields := strings.SplitN(string(raw[:nul]), " ", 2)
	if len(fields) != 2 || !Type(fields[0]).valid() {
		return nil, fmt.Errorf("corrupt object %s: bad header %q", oid, raw[:nul])
	}
	size, err := strconv.Atoi(fields[1])
	if err != nil || size < 0 {
		return nil, fmt.Errorf("corrupt object %s: bad size", oid)
	}
	data := raw[nul+1:]
	if len(data) != size {
		return nil, fmt.Errorf("corrupt object %s: header says %d bytes, got %d", oid, size, len(data))
	}
	// A corrupted file can still decompress to something well formed. Verify
	// the name against the content so bad data cannot be mistaken for an object.
	sum := sha1.Sum(raw)
	if hex.EncodeToString(sum[:]) != oid {
		return nil, fmt.Errorf("corrupt object %s: sha-1 mismatch", oid)
	}
	return &Object{Type: Type(fields[0]), Data: data}, nil
}
