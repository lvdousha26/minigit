package index

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lvdousha26/minigit/object"
)

func sampleEntry(path string) Entry {
	return Entry{
		CtimeSec: 1700000000,
		MtimeSec: 1700000001,
		Dev:      66306,
		Ino:      1234567,
		Mode:     ModeFile,
		UID:      1000,
		GID:      1000,
		Size:     6,
		OID:      object.Hash(object.Blob, []byte("hello\n")),
		Path:     path,
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index")
	ix := New()
	ix.Upsert(sampleEntry("src/main.go"))
	ix.Upsert(sampleEntry("a.txt"))
	if err := ix.Write(path); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("got %d entries", len(got.Entries))
	}
	// Write sorts by full path, so a.txt comes first.
	if got.Entries[0].Path != "a.txt" || got.Entries[1].Path != "src/main.go" {
		t.Fatalf("unexpected order: %v", got.Entries)
	}
	want := sampleEntry("a.txt")
	if got.Entries[0] != want {
		t.Fatalf("entry changed:\n got %+v\nwant %+v", got.Entries[0], want)
	}
}

func TestEntryAlignment(t *testing.T) {
	// Every entry must start on an 8-byte boundary and carry at least one
	// trailing NUL. Git rejects an index that violates either rule.
	path := filepath.Join(t.TempDir(), "index")
	ix := New()
	for _, p := range []string{"a", "ab", "abcdefg", "abcdefgh", "dir/with/a/long/name.txt"} {
		ix.Upsert(sampleEntry(p))
	}
	if err := ix.Write(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:4]) != "DIRC" || binary.BigEndian.Uint32(data[4:8]) != 2 {
		t.Fatal("bad header")
	}
	count := int(binary.BigEndian.Uint32(data[8:12]))
	off := headerSize
	for i := 0; i < count; i++ {
		if off%8 != headerSize%8 {
			t.Fatalf("entry %d starts at unaligned offset %d", i, off)
		}
		nameLen := int(binary.BigEndian.Uint16(data[off+60:off+62]) & 0x0fff)
		end := off + fixedSize + nameLen + padLen(nameLen)
		if data[end-1] != 0 {
			t.Fatalf("entry %d is not NUL terminated", i)
		}
		off = end
	}
	if len(data)-off != checksumLen {
		t.Fatalf("%d bytes left over after the entries, want only the checksum", len(data)-off)
	}
}

func TestChecksumIsVerified(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index")
	ix := New()
	ix.Upsert(sampleEntry("a.txt"))
	if err := ix.Write(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	data[30] ^= 0xff
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected a checksum error, got %v", err)
	}
}

func TestChecksumMatchesGitDefinition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index")
	ix := New()
	ix.Upsert(sampleEntry("a.txt"))
	if err := ix.Write(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	sum := sha1.Sum(data[:len(data)-checksumLen])
	if !bytes.Equal(sum[:], data[len(data)-checksumLen:]) {
		t.Fatal("trailer is not sha1 of the preceding bytes")
	}
}

func TestReadRejectsBadHeader(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"short", []byte("DIRC"), "too short"},
		{"signature", append([]byte("XXXX"), make([]byte, 30)...), "signature"},
		{"version", func() []byte {
			b := []byte("DIRC")
			b = binary.BigEndian.AppendUint32(b, 3)
			return append(b, make([]byte, 24)...)
		}(), "unsupported index version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, tc.name)
			if err := os.WriteFile(p, tc.data, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Read(p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}

func TestReadMissingIndexIsEmpty(t *testing.T) {
	ix, err := Read(filepath.Join(t.TempDir(), "index"))
	if err != nil || len(ix.Entries) != 0 {
		t.Fatalf("expected an empty index, got %v %v", ix, err)
	}
}

func TestWriteRejectsInvalidInput(t *testing.T) {
	dir := t.TempDir()
	badPath := New()
	badPath.Upsert(Entry{OID: sampleEntry("x").OID, Path: `a\b`})
	if err := badPath.Write(filepath.Join(dir, "a")); err == nil {
		t.Fatal("expected an error for a backslash path")
	}
	badOID := New()
	badOID.Upsert(Entry{OID: "nope", Path: "a"})
	if err := badOID.Write(filepath.Join(dir, "b")); err == nil {
		t.Fatal("expected an error for a malformed oid")
	}
	long := New()
	long.Upsert(Entry{OID: sampleEntry("x").OID, Path: strings.Repeat("n", 0x1000)})
	if err := long.Write(filepath.Join(dir, "c")); err == nil {
		t.Fatal("expected an error for an over-long path")
	}
}

func TestUpsertAndDelete(t *testing.T) {
	ix := New()
	ix.Upsert(sampleEntry("a"))
	ix.Upsert(Entry{OID: "changed", Path: "a"})
	if len(ix.Entries) != 1 || ix.Entries[0].OID != "changed" {
		t.Fatal("Upsert appended instead of replacing")
	}
	if e, ok := ix.Lookup("a"); !ok || e.OID != "changed" {
		t.Fatal("Lookup failed")
	}
	ix.Delete("a")
	if len(ix.Entries) != 0 {
		t.Fatal("Delete left an entry behind")
	}
}
