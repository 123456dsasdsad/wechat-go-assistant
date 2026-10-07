// Package artifacts reads only result files explicitly selected by a completed
// task. It never follows a result path outside that task's outputs directory.
package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Entry struct {
	Path, Name, SHA256 string
	Size               int64
}

func Read(dir string) ([]Entry, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.New("output_directory_unavailable")
	}
	defer root.Close()
	f, err := root.Open("outputs/manifest.json")
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("output_manifest_unreadable")
	}
	defer f.Close()
	var manifest struct {
		Files []string `json:"files"`
	}
	decoder := json.NewDecoder(io.LimitReader(f, 128*1024+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || len(manifest.Files) > 128 {
		return nil, errors.New("invalid_output_manifest")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("invalid_output_manifest")
	}
	seen := map[string]bool{}
	var entries []Entry
	for _, path := range manifest.Files {
		if len(path) > 1000 || strings.Contains(path, "\\") || !strings.HasPrefix(path, "outputs/") || !filepath.IsLocal(path) || filepath.ToSlash(filepath.Clean(path)) != path || path == "outputs/manifest.json" || seen[path] {
			return nil, errors.New("unsafe_output_path")
		}
		seen[path] = true
		// Reject all symlink components, including links that remain inside the root.
		components := strings.Split(path, "/")
		for i := range components {
			info, e := root.Lstat(filepath.Join(components[:i+1]...))
			if e != nil || info.Mode()&os.ModeSymlink != 0 {
				return nil, errors.New("unsafe_output_path")
			}
		}
		file, e := root.Open(path)
		if e != nil {
			return nil, errors.New("output_file_unreadable")
		}
		info, e := file.Stat()
		if e != nil || !info.Mode().IsRegular() {
			file.Close()
			return nil, errors.New("unsafe_output_path")
		}
		hash := sha256.New()
		size, e := io.Copy(hash, file)
		file.Close()
		if e != nil || size != info.Size() {
			return nil, errors.New("output_file_changed")
		}
		entries = append(entries, Entry{Path: path, Name: filepath.Base(path), Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))})
	}
	return entries, nil
}
