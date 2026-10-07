package files

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zipFixture(names []string, symlink bool) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, name := range names {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if symlink {
			h.SetMode(os.ModeSymlink | 0777)
		}
		w, _ := z.CreateHeader(h)
		w.Write([]byte("unique fixture 17*23"))
	}
	z.Close()
	return b.Bytes()
}
func refFixture(name string, b []byte) Ref {
	sum := sha256.Sum256(b)
	return Ref{ID: strings.Repeat("a", 24), Name: name, Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])}
}
func TestPrepareZIPIntegrityAndRetry(t *testing.T) {
	b := zipFixture([]string{"docs/readme.txt"}, false)
	r := refFixture("资料.zip", b)
	root := t.TempDir()
	path, err := Prepare(root, r, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path), "extracted/docs/readme.txt"))
	if err != nil || !bytes.Contains(content, []byte("unique fixture")) {
		t.Fatal(err)
	}
	if _, err = Prepare(root, r, bytes.NewReader(b)); err != nil {
		t.Fatal("identical retry failed", err)
	}
	if _, err = Prepare(t.TempDir(), r, strings.NewReader("tampered")); err == nil {
		t.Fatal("integrity mismatch accepted")
	}
}
func TestZIPRejectsUnsafeEntries(t *testing.T) {
	for _, names := range [][]string{{"../escape"}, {"/absolute"}, {"C:/escape"}, {"a\\..\\escape"}, {"a.txt", "a.txt"}, {"A.txt", "a.txt"}} {
		b := zipFixture(names, false)
		if _, err := Prepare(t.TempDir(), refFixture("test.zip", b), bytes.NewReader(b)); err == nil {
			t.Fatal("unsafe ZIP accepted", names)
		}
	}
	b := zipFixture([]string{"link"}, true)
	if _, err := Prepare(t.TempDir(), refFixture("test.zip", b), bytes.NewReader(b)); err == nil {
		t.Fatal("symlink accepted")
	}
	var raw bytes.Buffer
	z := zip.NewWriter(&raw)
	w, _ := z.Create("large.txt")
	io.CopyN(w, ioRepeater{}, 65<<20)
	z.Close()
	b = raw.Bytes()
	if _, err := Prepare(t.TempDir(), refFixture("bomb.zip", b), bytes.NewReader(b)); err == nil {
		t.Fatal("expansion limit not applied")
	}
	b = []byte("unsupported")
	if _, err := Prepare(t.TempDir(), refFixture("test.rar", b), bytes.NewReader(b)); err == nil {
		t.Fatal("unsupported archive accepted")
	}
}

func TestLargeZIPAndCancellation(t *testing.T) {
	archive, err := os.CreateTemp(t.TempDir(), "large-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	hash := sha256.New()
	zw := zip.NewWriter(io.MultiWriter(archive, hash))
	entry, err := zw.CreateHeader(&zip.FileHeader{Name: "payload.bin", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.CopyN(entry, ioRepeater{}, 80<<20); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	info, _ := archive.Stat()
	ref := Ref{ID: strings.Repeat("a", 24), Name: "large.zip", Size: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}
	archive.Seek(0, 0)
	dir := t.TempDir()
	prepared, err := Prepare(dir, ref, archive)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.Stat(filepath.Join(dir, prepared, "extracted/payload.bin"))
	if err != nil || payload.Size() != 80<<20 {
		t.Fatal("large entry was not extracted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	archive.Seek(0, 0)
	if _, err = PrepareContext(ctx, t.TempDir(), ref, archive); err != context.Canceled {
		t.Fatal("cancellation ignored", err)
	}
}

func TestProjectZIPCountsFilesAndDirectories(t *testing.T) {
	var raw bytes.Buffer
	z := zip.NewWriter(&raw)
	for i := 0; i < 98; i++ {
		_, err := z.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("group-%d/", i), Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 244; i++ {
		w, err := z.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("group-%d/file-%d.txt", i%98, i), Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(w, "project fixture")
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	data := raw.Bytes()
	path, err := Prepare(dir, refFixture("project.zip", data), bytes.NewReader(data))
	if err != nil {
		t.Fatal("normal 342-entry project rejected", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, path, "extracted/group-47/file-243.txt"))
	if err != nil || string(content) != "project fixture" {
		t.Fatal("project entries not extracted", err)
	}
}

func TestZIPMetadataCountGuard(t *testing.T) {
	var raw bytes.Buffer
	z := zip.NewWriter(&raw)
	for i := 0; i <= MaxZIPEntries; i++ {
		if _, err := z.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("directory-%d/", i), Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	data := raw.Bytes()
	if _, err := Prepare(t.TempDir(), refFixture("entries.zip", data), bytes.NewReader(data)); err == nil || err.Error() != "zip_entry_limit" {
		t.Fatal("metadata guard not enforced", err)
	}
}
