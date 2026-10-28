package dependencyadmission

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"strconv"
	"testing"

	"foundry/internal/dependencybundle"
)

// A second central directory in the first EOCD's comment can be selected by
// archive/zip even when its own comment ends before EOF.
func wheelCommentDirectory(t *testing.T, b []byte, rename bool, count uint16) []byte {
	t.Helper()
	b = append([]byte(nil), b...)
	end := len(b) - 22
	offset := int(binary.LittleEndian.Uint32(b[end+16:]))
	central := append([]byte(nil), b[offset:end]...)
	if rename {
		for pos := 0; pos < len(central); {
			nameLen := int(binary.LittleEndian.Uint16(central[pos+28:]))
			extraLen := int(binary.LittleEndian.Uint16(central[pos+30:]))
			commentLen := int(binary.LittleEndian.Uint16(central[pos+32:]))
			if string(central[pos+46:pos+46+nameLen]) == "safe.txt" {
				copy(central[pos+46:], "evil.txt")
			}
			pos += 46 + nameLen + extraLen + commentLen
		}
	}
	fakeEnd := append([]byte(nil), b[end:]...)
	binary.LittleEndian.PutUint32(fakeEnd[16:], uint32(len(b)))
	binary.LittleEndian.PutUint16(fakeEnd[8:], count)
	binary.LittleEndian.PutUint16(fakeEnd[10:], count)
	binary.LittleEndian.PutUint16(fakeEnd[20:], 0)
	comment := append(central, fakeEnd...)
	comment = append(comment, 0)
	binary.LittleEndian.PutUint16(b[end+20:], uint16(len(comment)))
	return append(b, comment...)
}

func TestPythonZIPCommentDirectoryDisagreement(t *testing.T) {
	files := wheelFiles("alpha", "1.0", "")
	files["safe.txt"] = []byte("resource")
	b := makeWheel(t, "alpha", "1.0", files, func(rows [][]string) [][]string {
		for _, row := range rows {
			if row[0] == "safe.txt" {
				row[0] = "evil.txt"
			}
		}
		return rows
	})
	b = wheelCommentDirectory(t, b, true, 7)
	total, entries := int64(0), 0
	w := pythonWheel{Name: "alpha", Version: "1.0", Size: int64(len(b)), SHA256: sum(b)}
	if admitted, err := admitWheel(context.Background(), w, b, &total, &entries); err == nil {
		t.Fatalf("accepted local safe.txt as central evil.txt: %q", admitted.files["evil.txt"])
	}
	store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
	r, err := ImportPython(context.Background(), store, tree, lock)
	if err == nil || r.Digest != "" {
		t.Fatalf("mismatched archive authorized publication: receipt=%+v err=%v", r, err)
	}
	got, err := os.ReadDir(store)
	if err != nil || len(got) != 0 {
		t.Fatalf("mismatched archive left store entries: %v %v", got, err)
	}
}

func TestPythonZIPCommentDirectoryPreallocation(t *testing.T) {
	b := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", ""), nil)
	for _, count := range []uint16{7, 10001, 65535} {
		t.Run(strconv.Itoa(int(count)), func(t *testing.T) {
			bad := wheelCommentDirectory(t, b, false, count)
			// The alternate EOCD must be rejected at preflight, before the library
			// can allocate from its unverified count or follow ZIP64 claims.
			if n, err := wheelZIPCount(bad); err == nil {
				t.Fatalf("alternate directory count %d reached decoding with preflight count %d", count, n)
			}
		})
	}
}

func TestPythonZIPArchiveComments(t *testing.T) {
	for _, comment := range []string{"wheel publisher comment", string(bytes.Repeat([]byte("a"), 65535))} {
		b := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", ""), nil)
		binary.LittleEndian.PutUint16(b[len(b)-2:], uint16(len(comment)))
		b = append(b, comment...)
		original := append([]byte(nil), b...)
		store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
		r, err := ImportPython(context.Background(), store, tree, lock)
		if err != nil {
			t.Fatal(err)
		}
		if err = r.Validate(); err != nil {
			t.Fatal(err)
		}
		if r.Packages[0].ArtifactSHA256 != sum(original) || r.Packages[0].ArtifactSize != int64(len(original)) || !bytes.Equal(b, original) {
			t.Fatal("original commented archive provenance lost or input mutated")
		}
		if err = dependencybundle.Verify(context.Background(), store, r.Bundle); err != nil {
			t.Fatal(err)
		}
		assertNoPythonStage(t, store)
	}
}

func TestPythonZIPCommentSignaturePolicy(t *testing.T) {
	b := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", ""), nil)
	comment := []byte("comment PK\x05\x06")
	binary.LittleEndian.PutUint16(b[len(b)-2:], uint16(len(comment)))
	b = append(b, comment...)
	if _, err := wheelZIPCount(b); err == nil {
		t.Fatal("accepted unsupported EOCD signature in archive comment")
	}
}

func TestPythonZIPCommentTamper(t *testing.T) {
	b := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", ""), nil)
	binary.LittleEndian.PutUint16(b[len(b)-2:], 1)
	b = append(b, 'a')
	store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
	b[len(b)-1] = 'b'
	write(t, tree, "alpha-1.0-py3-none-any.whl", b)
	r, err := ImportPython(context.Background(), store, tree, lock)
	if err == nil || r.Digest != "" {
		t.Fatalf("comment tamper authorized publication: receipt=%+v err=%v", r, err)
	}
	got, err := os.ReadDir(store)
	if err != nil || len(got) != 0 {
		t.Fatalf("tampered archive left store entries: %v %v", got, err)
	}
}
