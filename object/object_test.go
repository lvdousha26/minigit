package object

import (
	"compress/zlib"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHashKnownVector(t *testing.T) {
	// The blob id for "hello\n" is a fixed point shared with real Git:
	// git hash-object on a file containing hello\n prints this exact string.
	if got, want := Hash(Blob, []byte("hello\n")), "ce013625030ba8dba906f756967f9e9ca394464a"; got != want {
		t.Fatalf("Hash(hello) = %s, want %s", got, want)
	}
}

func TestSerialize(t *testing.T) {
	got := string(Serialize(Tree, []byte("ab")))
	if got != "tree 2\x00ab" {
		t.Fatalf("Serialize = %q", got)
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	data := []byte{0, 1, 2, 255, '\n', 0}
	oid, err := s.Write(Blob, data)
	if err != nil {
		t.Fatal(err)
	}
	if oid != Hash(Blob, data) {
		t.Fatalf("Write returned %s, hash is %s", oid, Hash(Blob, data))
	}
	obj, err := s.Read(oid)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Type != Blob || string(obj.Data) != string(data) {
		t.Fatalf("round trip changed the object: %q", obj)
	}
	if !s.Exists(oid) {
		t.Fatal("Exists returned false after Write")
	}
}

func TestWriteIsIdempotent(t *testing.T) {
	s := NewStore(t.TempDir())
	first, err := s.Write(Commit, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	info1, _ := os.Stat(s.Path(first))
	second, err := s.Write(Commit, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	info2, _ := os.Stat(s.Path(second))
	if first != second || info1.ModTime() != info2.ModTime() {
		t.Fatal("writing the same content twice changed the object")
	}
}

func TestWriteRejectsUnknownType(t *testing.T) {
	if _, err := NewStore(t.TempDir()).Write(Type("wat"), nil); err == nil {
		t.Fatal("expected an error for an unknown type")
	}
}

// writeLoose plants an arbitrary zlib stream under a chosen name. This lets
// the tests check that a readable stream is still rejected when its content
// does not match the file name it lives at.
func writeLoose(t *testing.T, s *Store, oid string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(s.Path(oid)), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(s.Path(oid))
	if err != nil {
		t.Fatal(err)
	}
	zw := zlib.NewWriter(f)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReadRejectsContentThatDoesNotMatchItsName(t *testing.T) {
	s := NewStore(t.TempDir())
	fake := "ce013625030ba8dba906f756967f9e9ca394464a"
	writeLoose(t, s, fake, Serialize(Blob, []byte("something else\n")))
	if _, err := s.Read(fake); err == nil || !strings.Contains(err.Error(), "sha-1 mismatch") {
		t.Fatalf("expected a sha-1 mismatch, got %v", err)
	}
}

func TestReadRejectsMalformedObjects(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want string
	}{
		{"no header terminator", []byte("blob 5 hello"), "missing header"},
		{"bad type", []byte("wat 1\x00x"), "bad header"},
		{"bad size", []byte("blob xyz\x00x"), "bad size"},
		{"size mismatch", []byte("blob 9\x00x"), "header says 9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore(t.TempDir())
			oid := "ce013625030ba8dba906f756967f9e9ca394464a"
			writeLoose(t, s, oid, tc.raw)
			if _, err := s.Read(oid); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}

func TestReadRejectsBrokenZlibAndBadIDs(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := os.MkdirAll(filepath.Join(s.Root, "ce"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := s.Path("ce013625030ba8dba906f756967f9e9ca394464a")
	if err := os.WriteFile(path, []byte("not zlib"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read("ce013625030ba8dba906f756967f9e9ca394464a"); err == nil {
		t.Fatal("expected a corrupt object error")
	}
	for _, id := range []string{"abc", "zz013625030ba8dba906f756967f9e9ca394464a"} {
		if _, err := s.Read(id); err == nil {
			t.Fatalf("expected an error for id %q", id)
		}
	}
}

func TestDecodeOID(t *testing.T) {
	raw, err := DecodeOID("ce013625030ba8dba906f756967f9e9ca394464a")
	if err != nil || OIDString(raw) != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Fatalf("DecodeOID/OIDString mismatch: %v %s", err, OIDString(raw))
	}
	if _, err := DecodeOID("abc"); err == nil {
		t.Fatal("expected an error for a short id")
	}
}
