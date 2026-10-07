package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"io"
	"sync"
)

var packageMu sync.Mutex

// Stream to the protected disk store. No original is omitted, resized or renamed
// ambiguously, and no partially built archive is committed on a read/hash error.
func ensureResultPackage(queue *jobs.Store, outputs *files.Store, j jobs.Job) (jobs.Job, error) {
	packageMu.Lock()
	defer packageMu.Unlock()
	if current, ok := queue.Snapshot(j.ID); ok {
		j = current
	}
	if len(j.Outputs) < 2 || j.MediaPackage.ID != "" {
		return j, nil
	}
	if outputs == nil {
		return j, errors.New("output_store_unavailable")
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	go func() {
		z := zip.NewWriter(writer)
		write := func(name string, data []byte) error {
			w, e := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
			if e != nil {
				return e
			}
			_, e = w.Write(data)
			return e
		}
		err := write("result.txt", []byte(questionIdentity(j)+"\n\n"+j.Result))
		manifest := []map[string]any{}
		for i, ref := range j.Outputs {
			if err != nil {
				break
			}
			name := fmt.Sprintf("files/%03d_%s", i+1, ref.Name)
			f, e := outputs.OpenBlob(ref)
			if e != nil {
				err = e
				break
			}
			w, e := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
			if e != nil {
				f.Close()
				err = e
				break
			}
			h := sha256.New()
			n, e := io.Copy(io.MultiWriter(w, h), f)
			f.Close()
			if e != nil {
				err = e
				break
			}
			if n != ref.Size || hex.EncodeToString(h.Sum(nil)) != ref.SHA256 {
				err = errors.New("output_integrity_failed")
				break
			}
			manifest = append(manifest, map[string]any{"path": name, "original_name": ref.Name, "bytes": ref.Size, "sha256": ref.SHA256})
		}
		if err == nil {
			data, e := json.MarshalIndent(manifest, "", "  ")
			err = e
			if err == nil {
				err = write("manifest.json", data)
			}
		}
		if err == nil {
			err = z.Close()
		}
		writer.CloseWithError(err)
	}()
	ref, err := outputs.Save(j.Owner, "complete-package-v1:"+j.ID, "全部结果_"+j.ID[:8]+".zip", reader)
	if err != nil {
		return j, err
	}
	if err = queue.SetMediaPackage(j.ID, ref); err != nil {
		return j, err
	}
	current, ok := queue.Snapshot(j.ID)
	if !ok {
		return j, errors.New("unknown_media_job")
	}
	return current, nil
}
