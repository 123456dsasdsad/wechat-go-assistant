package files

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Count files and directories together. This guards metadata/inode exhaustion
// while allowing normal project archives with more than a few hundred entries.
const MaxZIPEntries = 100000

// Prepare verifies a leased blob and stages it inside its task. Archive paths
// never determine the task directory, and archive programs are never run here.
func Prepare(taskDir string, ref Ref, reader io.Reader) (string, error) {
	return PrepareContext(context.Background(), taskDir, ref, reader)
}
func PrepareContext(ctx context.Context, taskDir string, ref Ref, reader io.Reader) (string, error) {
	if !ValidRef(ref) || reader == nil {
		return "", errors.New("invalid_attachment")
	}
	root, err := filepath.Abs(taskDir)
	if err != nil {
		return "", err
	}
	parent := filepath.Join(root, "inputs")
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	if info, e := os.Lstat(parent); e != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("invalid_attachment_directory")
	}
	budget, err := diskBudget(parent)
	if err != nil || ref.Size > budget {
		return "", errors.New("attachment_storage_full")
	}
	stage, err := os.MkdirTemp(parent, ".input-*")
	if err != nil {
		return "", err
	}
	if relative, e := filepath.Rel(parent, stage); e != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid_attachment_directory")
	}
	defer os.RemoveAll(stage)
	original := filepath.Join(stage, "original")
	if err = os.Mkdir(original, 0700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(filepath.Join(original, ref.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), contextReader{ctx, io.LimitReader(reader, ref.Size+1)})
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n != ref.Size || hex.EncodeToString(hash.Sum(nil)) != ref.SHA256 {
		return "", errors.New("attachment_integrity_failed")
	}
	name := strings.ToLower(ref.Name)
	if strings.HasSuffix(name, ".zip") {
		if err = extractZIP(ctx, filepath.Join(original, ref.Name), filepath.Join(stage, "extracted")); err != nil {
			return "", err
		}
	} else if strings.HasSuffix(name, ".7z") || strings.HasSuffix(name, ".rar") || strings.HasSuffix(name, ".tar") || strings.HasSuffix(name, ".gz") || strings.HasSuffix(name, ".bz2") || strings.HasSuffix(name, ".xz") {
		return "", errors.New("archive_format_unsupported")
	}
	target := filepath.Join(parent, ref.ID)
	metadata, _ := json.Marshal(ref)
	if err = os.WriteFile(filepath.Join(stage, "ref.json"), metadata, 0600); err != nil {
		return "", err
	}
	// A retry reuses an identical completed input; it never deletes existing data.
	if info, e := os.Lstat(target); e == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", errors.New("invalid_attachment_directory")
		}
		b, e := os.ReadFile(filepath.Join(target, "ref.json"))
		var existing Ref
		if e != nil || json.Unmarshal(b, &existing) != nil || existing != ref {
			return "", errors.New("attachment_snapshot_conflict")
		}
		return filepath.ToSlash(filepath.Join("inputs", ref.ID)), nil
	} else if !os.IsNotExist(e) {
		return "", e
	}
	if err = os.Rename(stage, target); err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join("inputs", ref.ID)), nil
}

func extractZIP(ctx context.Context, filename, directory string) error {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return errors.New("invalid_zip")
	}
	defer archive.Close()
	budget, err := diskBudget(filepath.Dir(directory))
	if err != nil {
		return err
	}
	info, err := os.Stat(filename)
	if err != nil {
		return err
	}
	// Budget against disk capacity; extreme compression still needs a bounded
	// expansion ratio. Normal large files have no fixed per-entry byte ceiling.
	if info.Size() < budget/1000 {
		ratioBudget := info.Size() * 1000
		if ratioBudget < 64<<20 {
			ratioBudget = 64 << 20
		}
		if ratioBudget < budget {
			budget = ratioBudget
		}
	}
	if len(archive.File) > MaxZIPEntries {
		return errors.New("zip_entry_limit")
	}
	seen := map[string]bool{}
	var declared uint64
	for _, entry := range archive.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if name == "" || strings.ContainsAny(name, "\\:\x00") || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || len(name) > 240 || strings.Count(name, "/") > 12 {
			return errors.New("zip_unsafe_path")
		}
		for _, part := range strings.Split(name, "/") {
			if !validName(part) {
				return errors.New("zip_unsafe_path")
			}
		}
		if seen[strings.ToLower(name)] {
			return errors.New("zip_duplicate_path")
		}
		seen[strings.ToLower(name)] = true
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || (!entry.FileInfo().IsDir() && !mode.IsRegular()) {
			return errors.New("zip_unsupported_entry")
		}
		if entry.UncompressedSize64 > uint64(budget)-declared {
			return errors.New("zip_expansion_limit")
		}
		declared += entry.UncompressedSize64
	}
	if err = os.Mkdir(directory, 0700); err != nil {
		return err
	}
	var total int64
	for _, entry := range archive.File {
		target := filepath.Join(directory, filepath.FromSlash(entry.Name))
		if entry.FileInfo().IsDir() {
			if err = os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		src, e := entry.Open()
		if e != nil {
			return errors.New("zip_entry_unreadable")
		}
		dst, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			src.Close()
			return errors.New("zip_path_conflict")
		}
		n, e := io.Copy(dst, contextReader{ctx, io.LimitReader(src, budget-total+1)})
		srcErr := src.Close()
		dstErr := dst.Close()
		total += n
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e != nil || srcErr != nil || dstErr != nil {
			return errors.New("zip_entry_unreadable")
		}
		if total > budget || uint64(n) != entry.UncompressedSize64 {
			return errors.New("zip_expansion_limit")
		}
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
