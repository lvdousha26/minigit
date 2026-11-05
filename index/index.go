// Package index reads and writes the v2 Git index (the staging area).
//
// The on-disk format is "DIRC", a big-endian version and entry count, a list
// of fixed-width stat records, then a SHA-1 of everything before it:
//
//	header:  DIRC | u32 version(2) | u32 count
//	entry:   10 x u32 stat | 20 byte oid | u16 flags | path | NUL padding
//	trailer: 20 byte sha1 of all preceding bytes
//
// Each entry is padded with at least one NUL so the next one starts on an
// 8-byte boundary. Paths always use forward slashes.
package index

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lvdousha26/minigit/object"
)

const (
	headerSize  = 12
	fixedSize   = 62
	checksumLen = 20
)

const (
	ModeFile = 0o100644
	ModeExec = 0o100755
)

type Entry struct {
	CtimeSec uint32
	MtimeSec uint32
	Dev      uint32
	Ino      uint32
	Mode     uint32
	UID      uint32
	GID      uint32
	Size     uint32
	OID      string // 40 hex characters
	Path     string // forward slashes, relative to the work tree
	// CtimeNsec, MtimeNsec and stage are always zero here.
}

type Index struct {
	Entries []Entry
}

func New() *Index { return &Index{} }

func (ix *Index) Lookup(path string) (Entry, bool) {
	for _, e := range ix.Entries {
		if e.Path == path {
			return e, true
		}
	}
	return Entry{}, false
}

func (ix *Index) Upsert(e Entry) {
	for i := range ix.Entries {
		if ix.Entries[i].Path == e.Path {
			ix.Entries[i] = e
			return
		}
	}
	ix.Entries = append(ix.Entries, e)
}

func (ix *Index) Delete(path string) {
	out := ix.Entries[:0]
	for _, e := range ix.Entries {
		if e.Path != path {
			out = append(out, e)
		}
	}
	ix.Entries = out
}

// Sort orders entries by the full path string, which is what Git's
// cache_name_compare does. Tree entries follow a different rule: a directory
// is compared as if its name ended in '/'. See tree.WriteTree.
func (ix *Index) Sort() {
	sort.Slice(ix.Entries, func(i, j int) bool {
		return ix.Entries[i].Path < ix.Entries[j].Path
	})
}

func padLen(nameLen int) int {
	return 8 - ((fixedSize + nameLen) % 8)
}

func Read(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, err
	}
	if len(data) < headerSize+checksumLen {
		return nil, fmt.Errorf("index too short: %d bytes", len(data))
	}
	if string(data[:4]) != "DIRC" {
		return nil, fmt.Errorf("bad index signature %q", data[:4])
	}
	version := binary.BigEndian.Uint32(data[4:8])
	if version != 2 {
		return nil, fmt.Errorf("unsupported index version %d (only v2)", version)
	}
	sum := sha1.Sum(data[:len(data)-checksumLen])
	if !bytes.Equal(sum[:], data[len(data)-checksumLen:]) {
		return nil, fmt.Errorf("index checksum mismatch")
	}

	count := binary.BigEndian.Uint32(data[8:12])
	body := data[headerSize : len(data)-checksumLen]
	ix := New()
	off := 0
	for n := uint32(0); n < count; n++ {
		e, next, err := readEntry(body, off)
		if err != nil {
			return nil, fmt.Errorf("index entry %d: %w", n, err)
		}
		ix.Entries = append(ix.Entries, e)
		off = next
	}
	// Bytes between the last entry and the checksum are index extensions
	// (TREE, REUC, ...). We do not produce them but tolerate them.
	return ix, nil
}

func readEntry(b []byte, off int) (Entry, int, error) {
	if off+fixedSize > len(b) {
		return Entry{}, 0, fmt.Errorf("truncated fixed fields")
	}
	head := b[off : off+fixedSize]
	be := binary.BigEndian
	e := Entry{
		CtimeSec: be.Uint32(head[0:4]),
		MtimeSec: be.Uint32(head[8:12]),
		Dev:      be.Uint32(head[16:20]),
		Ino:      be.Uint32(head[20:24]),
		Mode:     be.Uint32(head[24:28]),
		UID:      be.Uint32(head[28:32]),
		GID:      be.Uint32(head[32:36]),
		Size:     be.Uint32(head[36:40]),
		OID:      hex.EncodeToString(head[40:60]),
	}
	p := off + fixedSize
	nameLen := int(be.Uint16(head[60:62]) & 0x0fff)

	if nameLen == 0x0fff {
		// Long paths are NUL terminated instead of length prefixed.
		z := bytes.IndexByte(b[p:], 0)
		if z < 0 {
			return Entry{}, 0, fmt.Errorf("unterminated long path")
		}
		e.Path = string(b[p : p+z])
		p += z + 1 + padLen(z)
	} else {
		end := p + nameLen + padLen(nameLen)
		if end > len(b) {
			return Entry{}, 0, fmt.Errorf("truncated path or padding")
		}
		e.Path = string(b[p : p+nameLen])
		p = end
	}
	if p > len(b) {
		return Entry{}, 0, fmt.Errorf("truncated padding")
	}
	return e, p, nil
}

func (ix *Index) Write(path string) error {
	ix.Sort()
	var buf bytes.Buffer
	buf.WriteString("DIRC")
	writeU32(&buf, 2)
	writeU32(&buf, uint32(len(ix.Entries)))

	for _, e := range ix.Entries {
		if len(e.Path) > 0x0fff {
			return fmt.Errorf("path too long: %s", e.Path)
		}
		if strings.Contains(e.Path, "\\") || strings.HasPrefix(e.Path, "/") || e.Path == "" {
			return fmt.Errorf("bad index path %q", e.Path)
		}
		raw, err := object.DecodeOID(e.OID)
		if err != nil {
			return err
		}
		writeU32(&buf, e.CtimeSec)
		writeU32(&buf, 0) // ctime nanoseconds
		writeU32(&buf, e.MtimeSec)
		writeU32(&buf, 0) // mtime nanoseconds
		writeU32(&buf, e.Dev)
		writeU32(&buf, e.Ino)
		writeU32(&buf, e.Mode)
		writeU32(&buf, e.UID)
		writeU32(&buf, e.GID)
		writeU32(&buf, e.Size)
		buf.Write(raw[:])
		writeU16(&buf, uint16(len(e.Path)))
		buf.WriteString(e.Path)
		buf.Write(make([]byte, padLen(len(e.Path))))
	}
	sum := sha1.Sum(buf.Bytes())
	buf.Write(sum[:])

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "index_tmp_*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Windows refuses to rename over an existing file.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmpName, path)
}

func writeU32(b *bytes.Buffer, v uint32) {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	b.Write(tmp[:])
}

func writeU16(b *bytes.Buffer, v uint16) {
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], v)
	b.Write(tmp[:])
}
