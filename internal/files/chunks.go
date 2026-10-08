package files

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"io"
	"os"
	"path/filepath"
	"time"
)

const ChunkBytes = 4 << 20

type Upload struct {
	ID      string    `json:"id"`
	Owner   string    `json:"-"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Offset  int64     `json:"offset"`
	Created time.Time `json:"created"`
	File    Ref       `json:"file"`
}
type uploadRecord struct {
	Upload
	OwnerID string `json:"owner"`
}

func (s *Store) uploadPath() string { return filepath.Join(s.root, "chunks", "index.json") }
func (s *Store) uploads() (map[string]uploadRecord, error) {
	m := map[string]uploadRecord{}
	b, e := metadb.ReadJSON(s.uploadPath())
	if os.IsNotExist(e) {
		return m, nil
	}
	if e != nil {
		return nil, e
	}
	if json.Unmarshal(b, &m) != nil {
		return nil, errors.New("invalid_upload_index")
	}
	return m, nil
}
func (s *Store) BeginUpload(owner, key, name string, size int64) (Upload, error) {
	s.chunkMu.Lock()
	defer s.chunkMu.Unlock()
	if owner == "" || !validHex(key, 24) || !validName(name) || size < 0 {
		return Upload{}, errors.New("invalid_upload")
	}
	m, e := s.uploads()
	if e != nil {
		return Upload{}, e
	}
	id := digest(owner + "\x00" + key)[:24]
	if v, ok := m[id]; ok && s.now().Sub(v.Created) <= 7*24*time.Hour {
		if v.OwnerID != owner || v.Name != name || v.Size != size {
			return Upload{}, errors.New("upload_conflict")
		}
		return v.Upload, nil
	}
	for k, v := range m {
		if s.now().Sub(v.Created) > 7*24*time.Hour {
			delete(m, k)
			os.Remove(filepath.Join(s.root, "chunks", k+".part"))
		}
	}
	if len(m) >= 64 {
		return Upload{}, errors.New("too_many_uploads")
	}
	if e = os.MkdirAll(filepath.Dir(s.uploadPath()), 0700); e != nil {
		return Upload{}, e
	}
	available, e := s.space(s.root)
	if e != nil || available-ReserveDiskBytes < size {
		return Upload{}, errors.New("file_storage_full")
	}
	v := Upload{ID: id, Name: name, Size: size, Created: s.now().UTC()}
	m[id] = uploadRecord{v, owner}
	e = metadb.WriteJSON(s.uploadPath(), m)
	return v, e
}
func (s *Store) AppendUpload(owner, id string, offset int64, r io.Reader) (Upload, error) {
	s.chunkMu.Lock()
	defer s.chunkMu.Unlock()
	m, e := s.uploads()
	if e != nil {
		return Upload{}, e
	}
	v, ok := m[id]
	if !ok || !validHex(id, 24) || v.OwnerID != owner || s.now().Sub(v.Created) > 7*24*time.Hour {
		return Upload{}, errors.New("upload_not_found")
	}
	if v.File.ID != "" {
		return v.Upload, nil
	}
	if offset < 0 || offset > v.Offset {
		return v.Upload, errors.New("upload_offset_conflict")
	}
	data, e := io.ReadAll(io.LimitReader(r, ChunkBytes+1))
	if e != nil {
		return v.Upload, e
	}
	if len(data) > ChunkBytes || int64(len(data))+offset > v.Size {
		return v.Upload, errors.New("invalid_chunk_size")
	}
	f, e := os.OpenFile(filepath.Join(s.root, "chunks", id+".part"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return v.Upload, e
	}
	defer f.Close()
	if offset < v.Offset {
		if offset+int64(len(data)) > v.Offset {
			return v.Upload, errors.New("upload_offset_conflict")
		}
		old := make([]byte, len(data))
		_, e = f.ReadAt(old, offset)
		if e != nil || !bytes.Equal(data, old) {
			return v.Upload, errors.New("upload_chunk_conflict")
		}
		return v.Upload, nil
	}
	available, e := s.space(s.root)
	if e != nil || available-ReserveDiskBytes < int64(len(data)) {
		return v.Upload, errors.New("file_storage_full")
	}
	if e = f.Truncate(v.Offset); e != nil {
		return v.Upload, e
	}
	if _, e = f.WriteAt(data, offset); e != nil {
		return v.Upload, e
	}
	if e = f.Sync(); e != nil {
		return v.Upload, e
	}
	v.Offset += int64(len(data))
	m[id] = v
	e = metadb.WriteJSON(s.uploadPath(), m)
	return v.Upload, e
}
func (s *Store) FinishUpload(owner, id string) (Ref, error) {
	s.chunkMu.Lock()
	defer s.chunkMu.Unlock()
	m, e := s.uploads()
	if e != nil {
		return Ref{}, e
	}
	v, ok := m[id]
	if !ok || !validHex(id, 24) || v.OwnerID != owner {
		return Ref{}, errors.New("upload_not_found")
	}
	if v.File.ID != "" {
		return s.Get(owner, v.File.ID)
	}
	if v.Offset != v.Size {
		return Ref{}, errors.New("upload_incomplete")
	}
	f, e := os.OpenFile(filepath.Join(s.root, "chunks", id+".part"), os.O_CREATE|os.O_RDONLY, 0600)
	if e != nil {
		return Ref{}, e
	}
	defer f.Close()
	ref, e := s.Save(owner, "chunk:"+id, v.Name, f)
	if e != nil {
		return Ref{}, e
	}
	v.File = ref
	m[id] = v
	if e = metadb.WriteJSON(s.uploadPath(), m); e != nil {
		return Ref{}, e
	}
	f.Close()
	os.Remove(filepath.Join(s.root, "chunks", id+".part"))
	return ref, nil
}
func (s *Store) Delete(owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.state.Files[id]
	if !ok || v.Owner != owner {
		return errors.New("file_not_found")
	}
	next := s.copyState()
	delete(next.Files, id)
	if e := s.save(next); e != nil {
		return e
	}
	e := os.Remove(filepath.Join(s.root, id+".bin"))
	if os.IsNotExist(e) {
		return nil
	}
	return e
}

func (s *Store) Revoke(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyState()
	hash := digest(token)
	for k, v := range next.Grants {
		if v.Hash == hash {
			delete(next.Grants, k)
		}
	}
	return s.save(next)
}
