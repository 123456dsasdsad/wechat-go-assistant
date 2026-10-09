package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/artifacts"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
)

// Register only the two explicitly requested material action files. Packaging
// must not depend on the model remembering to write a manifest.
func registerActionOutputs(task jobs.Task, dir string) error {
	if !strings.Contains(task.Input, "请提取行动清单：") {
		return nil
	}
	material := false
	for _, ref := range task.Attachments {
		material = material || strings.HasPrefix(ref.Name, "材料来源-") && strings.HasSuffix(ref.Name, ".json")
	}
	if !material {
		return nil
	}
	entries, err := artifacts.Read(dir)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return errors.New("output_directory_unavailable")
	}
	defer root.Close()
	if err = root.Mkdir("outputs", 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := root.Lstat("outputs")
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe_output_path")
	}
	paths := []string{}
	seen := map[string]bool{}
	for _, entry := range entries {
		paths = append(paths, entry.Path)
		seen[entry.Path] = true
	}
	for _, name := range []string{"行动清单.md", "行动清单.json"} {
		path := "outputs/" + name
		if seen[path] {
			continue
		}
		info, err := root.Lstat(path)
		if os.IsNotExist(err) {
			info, err = root.Lstat(name)
			if os.IsNotExist(err) {
				continue // Never manufacture a result the model did not produce.
			}
			if err != nil || !info.Mode().IsRegular() {
				return errors.New("unsafe_output_path")
			}
			limit := int64(1 << 20)
			if strings.HasSuffix(name, ".json") {
				limit = 256 << 10
			}
			if info.Size() > limit {
				return errors.New("action_output_too_large")
			}
			src, err := root.Open(name)
			if err != nil {
				return err
			}
			dst, err := root.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				src.Close()
				return err
			}
			size, copyErr := io.Copy(dst, io.LimitReader(src, limit+1))
			src.Close()
			closeErr := dst.Close()
			if copyErr != nil || closeErr != nil || size != info.Size() {
				root.Remove(path)
				return errors.New("output_file_changed")
			}
		} else if err != nil || !info.Mode().IsRegular() {
			return errors.New("unsafe_output_path")
		}
		paths = append(paths, path)
	}
	if len(paths) == len(entries) {
		return nil
	}
	if len(paths) > 128 {
		return errors.New("invalid_output_manifest")
	}
	data, _ := json.Marshal(struct {
		Files []string `json:"files"`
	}{paths})
	// An exclusive temporary file prevents following a pre-existing link.
	const temp = "outputs/.action-manifest.tmp"
	f, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = root.Rename(temp, "outputs/manifest.json"); err != nil {
		return err
	}
	_, err = artifacts.Read(dir)
	return err
}
