package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestStagedReleaseCacheMustMatchOfficialSizeAndDigest(t *testing.T) {
	for _, cached := range []string{"release bytes", "corrupt bytes"} {
		t.Run(cached, func(t *testing.T) {
			root := t.TempDir()
			data := "release bytes"
			digest := sha256.Sum256([]byte(data))
			name := "codex-package-x86_64-unknown-linux-musl.tar.gz"
			assetURL := "https://github.com/openai/codex/releases/download/rust-v0.161.0/" + name
			release, _ := json.Marshal(Release{Tag: "rust-v0.161.0", Assets: []Asset{{Name: name, URL: assetURL, Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(digest[:])}}})
			if err := os.WriteFile(filepath.Join(root, name), []byte(cached), 0600); err != nil {
				t.Fatal(err)
			}
			downloads := 0
			client := fakeDoer{func(r *http.Request) (*http.Response, error) {
				if r.URL.String() == assetURL {
					downloads++
					return response(200, data), nil
				}
				return response(200, string(release)), nil
			}}
			updates, err := (Fetcher{Client: client}).Stage(context.Background(), []Program{{Name: "Codex CLI", Repo: "openai/codex", Version: "0.160.1", AssetPattern: "^" + name + "$"}}, root)
			if err != nil || len(updates) != 1 || updates[0].State != "已校验，等待安装" {
				t.Fatalf("stage failed: %+v %v", updates, err)
			}
			want := 0
			if cached != data {
				want = 1
			}
			if downloads != want {
				t.Fatalf("downloads=%d, want %d", downloads, want)
			}
			actual, _ := os.ReadFile(updates[0].Archive)
			if string(actual) != data {
				t.Fatal("unverified cached bytes retained")
			}
		})
	}
}
