package files

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *Store) taskSignature(owner, id string, expiry int64) string {
	h := hmac.New(sha256.New, s.key)
	fmt.Fprintf(h, "task\x00%s\x00%s\x00%d", owner, id, expiry)
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Store) TaskLink(publicURL, owner, id string) (string, error) {
	u, err := url.Parse(publicURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !validHex(id, 24) || owner == "" {
		return "", errors.New("invalid_task_link")
	}
	expiry := s.now().Add(24 * time.Hour).Unix()
	u.Path = strings.TrimRight(u.Path, "/") + "/task/" + id
	u.RawQuery = url.Values{"e": {strconv.FormatInt(expiry, 10)}, "token": {s.taskSignature(owner, id, expiry)}}.Encode()
	return u.String(), nil
}
func (s *Store) AuthorizeTask(owner, id, expiryText, token string) bool {
	expiry, err := strconv.ParseInt(expiryText, 10, 64)
	now := s.now()
	return owner != "" && validHex(id, 24) && err == nil && expiry > now.Unix() && expiry <= now.Add(24*time.Hour+time.Minute).Unix() && validHex(token, 64) && hmac.Equal([]byte(token), []byte(s.taskSignature(owner, id, expiry)))
}
