package artifacts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestResultIntegrityAndBoundaries(t *testing.T) {
	d := t.TempDir()
	os.Mkdir(filepath.Join(d, "outputs"), 0700)
	os.WriteFile(filepath.Join(d, "outputs", "figure.png"), []byte("real output"), 0600)
	manifest := filepath.Join(d, "outputs", "manifest.json")
	os.WriteFile(manifest, []byte(`{"files":["outputs/figure.png"]}`), 0600)
	refs, e := Read(d)
	if e != nil || len(refs) != 1 || refs[0].Size != 11 || len(refs[0].SHA256) != 64 {
		t.Fatal(refs, e)
	}
	for _, text := range []string{`{"files":["outputs/../secret"]}`, `{"files":["/etc/passwd"]}`, `{"files":["outputs/figure.png","outputs/figure.png"]}`, `{"files":["outputs/manifest.json"]}`, `{"files":["outputs/missing"]}`, `{"files":["outputs/figure.png"],"secret":true}`, `{"files":[]} {}`} {
		os.WriteFile(manifest, []byte(text), 0600)
		if _, e = Read(d); e == nil {
			t.Fatal("unsafe manifest accepted", text)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("private"), 0600)
	if os.Symlink(outside, filepath.Join(d, "outputs", "link")) == nil {
		os.WriteFile(manifest, []byte(`{"files":["outputs/link"]}`), 0600)
		if _, e = Read(d); e == nil {
			t.Fatal("symlink accepted")
		}
	}
}
