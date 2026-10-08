package files

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const MaxFiles = 64

// Management devices last up to 30 days; short-lived upload/management links
// share this store. Keep a bounded capacity without exhausting it after a few
// browser logins and ordinary upload links.
const MaxGrants = 128

type verifiedReader struct {
	reader         io.Reader
	digest         hash.Hash
	size, expected int64
	sha            string
}

func (r *verifiedReader) Read(p []byte) (int, error) {
	n, e := r.reader.Read(p)
	r.digest.Write(p[:n])
	r.size += int64(n)
	if r.size > r.expected {
		return n, errors.New("output_integrity_failed")
	}
	if e == io.EOF && (r.size != r.expected || hex.EncodeToString(r.digest.Sum(nil)) != r.sha) {
		return n, errors.New("output_integrity_failed")
	}
	return n, e
}
func (s *Store) SaveVerified(owner, source, name string, reader io.Reader, size int64, sha string) (Ref, error) {
	if size < 0 || !validHex(sha, 64) {
		return Ref{}, errors.New("invalid_output_integrity")
	}
	ref, e := s.Save(owner, source, name, &verifiedReader{reader: reader, digest: sha256.New(), expected: size, sha: sha})
	if e == nil && (ref.Size != size || ref.SHA256 != sha || ref.Name != name) {
		return Ref{}, errors.New("output_integrity_failed")
	}
	return ref, e
}

type Ref struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type record struct {
	Ref
	Owner   string    `json:"owner"`
	Source  string    `json:"source"`
	Created time.Time `json:"created"`
}
type grant struct {
	Owner   string    `json:"owner"`
	Hash    string    `json:"hash"`
	Expires time.Time `json:"expires"`
}
type state struct {
	Version int               `json:"version"`
	Files   map[string]record `json:"files"`
	Grants  map[string]grant  `json:"grants"`
}
type Store struct {
	mu       sync.Mutex
	chunkMu  sync.Mutex
	root     string
	key      []byte
	state    state
	now      func() time.Time
	space    func(string) (int64, error)
	maxFiles int
}

func ValidRef(r Ref) bool {
	return validHex(r.ID, 24) && validHex(r.SHA256, 64) && r.Size >= 0 && r.Size < math.MaxInt64 && validName(r.Name)
}
func validHex(s string, length int) bool {
	if len(s) != length {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && s == strings.ToLower(s)
}
func validName(name string) bool {
	if name == "" || len(name) > 180 || name == "." || name == ".." || strings.ContainsAny(name, "/\\:") {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return strings.TrimSpace(name) == name
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func Open(root string) (*Store, error) {
	return OpenWithLimit(root, MaxFiles)
}

// OpenWithLimit gives the separate result store a larger metadata capacity;
// actual disk headroom still determines how many bytes may be written.
func OpenWithLimit(root string, limit int) (*Store, error) {
	if limit < 1 || limit > 4096 {
		return nil, errors.New("invalid_file_capacity")
	}
	if root == "" {
		return nil, errors.New("files_directory_required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	s := &Store{root: root, now: time.Now, space: availableDisk, maxFiles: limit, state: state{Version: 1, Files: map[string]record{}, Grants: map[string]grant{}}}
	keyPath := filepath.Join(root, "grant-secret.key")
	key, err := os.ReadFile(keyPath)
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		err = os.WriteFile(keyPath, key, 0600)
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("upload_grant_key_unreadable")
	}
	s.key = key
	b, err := metadb.ReadJSON(filepath.Join(root, "index.json"))
	if err == nil {
		if len(b) > 4<<20 || json.Unmarshal(b, &s.state) != nil || s.state.Version != 1 || s.state.Files == nil || s.state.Grants == nil {
			return nil, errors.New("invalid_files_index")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for id, r := range s.state.Files {
		if id != r.ID || !ValidRef(r.Ref) || r.Owner == "" || r.Source == "" {
			return nil, errors.New("invalid_saved_file")
		}
	}
	if len(s.state.Files) > s.maxFiles || len(s.state.Grants) > MaxGrants {
		return nil, errors.New("invalid_files_index_limits")
	}
	for id, g := range s.state.Grants {
		if !validHex(id, 64) || !validHex(g.Hash, 64) || g.Owner == "" || g.Expires.IsZero() {
			return nil, errors.New("invalid_saved_upload_grant")
		}
	}
	if err = metadb.WriteJSON(filepath.Join(root, "index.json"), s.state); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Store) save(next state) error {
	if err := metadb.WriteJSON(filepath.Join(s.root, "index.json"), next); err != nil {
		return err
	}
	s.state = next
	return nil
}
func (s *Store) copyState() state {
	n := state{Version: 1, Files: map[string]record{}, Grants: map[string]grant{}}
	for k, v := range s.state.Files {
		n.Files[k] = v
	}
	for k, v := range s.state.Grants {
		n.Grants[k] = v
	}
	return n
}
func (s *Store) Save(owner, source, name string, reader io.Reader) (Ref, error) {
	if owner == "" || source == "" || reader == nil || !validName(name) {
		return Ref{}, errors.New("invalid_upload")
	}
	sourceID := digest(owner + "\x00" + source)
	s.mu.Lock()
	for _, r := range s.state.Files {
		if r.Owner == owner && r.Source == sourceID && s.now().Sub(r.Created) <= 7*24*time.Hour {
			s.mu.Unlock()
			return r.Ref, nil
		}
	}
	active := 0
	for _, r := range s.state.Files {
		if s.now().Sub(r.Created) <= 7*24*time.Hour {
			active++
		}
	}
	s.mu.Unlock()
	if active >= s.maxFiles {
		return Ref{}, errors.New("file_storage_full")
	}
	available, err := s.space(s.root)
	if err != nil || available <= ReserveDiskBytes {
		return Ref{}, errors.New("file_storage_full")
	}
	budget := available - ReserveDiskBytes
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Ref{}, err
	}
	id := hex.EncodeToString(random[:])
	f, err := os.CreateTemp(s.root, ".blob-*")
	if err != nil {
		return Ref{}, err
	}
	defer os.Remove(f.Name())
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(reader, budget+1))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return Ref{}, err
	}
	if closeErr != nil {
		return Ref{}, closeErr
	}
	if n > budget {
		return Ref{}, errors.New("file_storage_full")
	}
	available, err = s.space(s.root)
	if err != nil || available < ReserveDiskBytes {
		return Ref{}, errors.New("file_storage_full")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.state.Files {
		if r.Owner == owner && r.Source == sourceID && s.now().Sub(r.Created) <= 7*24*time.Hour {
			return r.Ref, nil
		}
	}
	next := s.copyState()
	for id, r := range next.Files {
		if s.now().Sub(r.Created) > 7*24*time.Hour {
			delete(next.Files, id)
		}
	}
	if len(next.Files) >= s.maxFiles {
		return Ref{}, errors.New("file_storage_full")
	}
	ref := Ref{ID: id, Name: name, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))}
	if err = os.Rename(f.Name(), filepath.Join(s.root, id+".bin")); err != nil {
		return Ref{}, err
	}
	next.Files[id] = record{Ref: ref, Owner: owner, Source: sourceID, Created: s.now().UTC()}
	previous := s.state.Files
	if err = s.save(next); err != nil {
		os.Remove(filepath.Join(s.root, id+".bin"))
		return Ref{}, err
	}
	for oldID := range previous {
		if _, kept := next.Files[oldID]; !kept {
			os.Remove(filepath.Join(s.root, oldID+".bin"))
		}
	}
	return ref, nil
}
func (s *Store) List(owner string) []Ref {
	s.mu.Lock()
	defer s.mu.Unlock()
	var records []record
	for _, r := range s.state.Files {
		if r.Owner == owner && s.now().Sub(r.Created) <= 7*24*time.Hour {
			records = append(records, r)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Created.After(records[j].Created) })
	if len(records) > 10 {
		records = records[:10]
	}
	refs := []Ref{}
	for _, r := range records {
		refs = append(refs, r.Ref)
	}
	return refs
}
func (s *Store) Get(owner, id string) (Ref, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.state.Files[id]
	if !ok || r.Owner != owner || s.now().Sub(r.Created) > 7*24*time.Hour {
		return Ref{}, errors.New("file_not_found")
	}
	return r.Ref, nil
}
func (s *Store) OpenBlob(ref Ref) (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.state.Files[ref.ID]
	if !ok || !ValidRef(ref) || r.Ref != ref || s.now().Sub(r.Created) > 7*24*time.Hour {
		return nil, errors.New("file_not_found")
	}
	return os.Open(filepath.Join(s.root, ref.ID+".bin"))
}

func (s *Store) Prune() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyState()
	var retired []string
	for id, r := range next.Files {
		if s.now().Sub(r.Created) > 7*24*time.Hour {
			delete(next.Files, id)
			retired = append(retired, id)
		}
	}
	for id, g := range next.Grants {
		if !s.now().Before(g.Expires) {
			delete(next.Grants, id)
		}
	}
	if len(next.Files) == len(s.state.Files) && len(next.Grants) == len(s.state.Grants) {
		return nil
	}
	if err := s.save(next); err != nil {
		return err
	}
	for _, id := range retired {
		if err := os.Remove(filepath.Join(s.root, id+".bin")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
func (s *Store) token(source string, g grant) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(source + "\x00" + g.Owner + "\x00" + g.Expires.Format(time.RFC3339Nano)))
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Store) Grant(owner, source string, duration ...time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner == "" || source == "" {
		return "", errors.New("invalid_grant")
	}
	id := digest(owner + "\x00" + source)
	if g, ok := s.state.Grants[id]; ok && s.now().Before(g.Expires) {
		return s.token(id, g), nil
	}
	next := s.copyState()
	for k, g := range next.Grants {
		if !s.now().Before(g.Expires) {
			delete(next.Grants, k)
		}
	}
	if len(next.Grants) >= MaxGrants {
		return "", errors.New("too_many_upload_links")
	}
	ttl := 30 * time.Minute
	if len(duration) > 0 && duration[0] > 0 && duration[0] <= 30*24*time.Hour {
		ttl = duration[0]
	}
	g := grant{Owner: owner, Expires: s.now().Add(ttl).UTC()}
	token := s.token(id, g)
	g.Hash = digest(token)
	next.Grants[id] = g
	if err := s.save(next); err != nil {
		return "", err
	}
	return token, nil
}
func (s *Store) Authorize(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validHex(token, 64) {
		return "", false
	}
	hash := digest(token)
	for _, g := range s.state.Grants {
		if s.now().Before(g.Expires) && subtle.ConstantTimeCompare([]byte(hash), []byte(g.Hash)) == 1 {
			return g.Owner, true
		}
	}
	return "", false
}
