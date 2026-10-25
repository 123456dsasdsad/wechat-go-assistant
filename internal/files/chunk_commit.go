package files

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Adopt an already fsynced upload on the same filesystem without making a second
// full-size copy. The deterministic target allows recovery after rename and before
// metadata commit. chunkMu is held by the caller.
func (s *Store) commitUpload(owner string, v uploadRecord) (Ref, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	id := digest("chunk-blob:" + owner + ":" + v.ID)[:24]
	part := filepath.Join(s.root, "chunks", v.ID+".part")
	target := filepath.Join(s.root, id+".bin")
	source := part
	if _, err := os.Stat(part); os.IsNotExist(err) {
		if _, err = os.Stat(target); err == nil {
			source = target
		} else if v.Size == 0 {
			f, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return Ref{}, err
			}
			f.Close()
		} else {
			return Ref{}, errors.New("upload_file_missing")
		}
	}
	f, err := os.Open(source)
	if err != nil {
		return Ref{}, err
	}
	h := sha256.New()
	size, err := io.Copy(h, f)
	f.Close()
	if err != nil || size != v.Size {
		return Ref{}, errors.New("upload_size_mismatch")
	}
	ref := Ref{ID: id, Name: v.Name, Size: size, SHA256: hex.EncodeToString(h.Sum(nil))}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.state.Files[id]; ok {
		if existing.Ref != ref || existing.Owner != owner {
			return Ref{}, errors.New("upload_file_conflict")
		}
		return ref, nil
	}
	next := s.copyState()
	if len(next.Files) >= s.maxFiles {
		return Ref{}, errors.New("file_storage_full")
	}
	if source != target {
		if _, err = os.Stat(target); err == nil {
			return Ref{}, errors.New("upload_target_conflict")
		}
		if err = os.Rename(source, target); err != nil {
			return Ref{}, err
		}
	}
	next.Files[id] = record{Ref: ref, Owner: owner, Conversation: v.Conversation, Source: digest(owner + "\x00chunk:" + v.ID), Created: s.now().UTC()}
	return ref, s.save(next)
}
