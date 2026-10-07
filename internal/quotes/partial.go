package quotes

import (
	"crypto/md5" // Protocol selection checksum, not a security primitive.
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"strings"
)

func nth(text, value string, n, from int) int {
	if value == "" || n < 0 || from < 0 || from > len(text) {
		return -1
	}
	for i := 0; i <= n; i++ {
		p := strings.Index(text[from:], value)
		if p < 0 {
			return -1
		}
		from += p
		if i == n {
			return from
		}
		from += len(value)
	}
	return -1
}

// Partial accepts both observed end-index variants, with MD5 disambiguation.
func Partial(text string, p *weixin.PartialText) (string, bool) {
	if p == nil {
		return text, true
	}
	start := nth(text, p.Start, p.StartIndex, 0)
	if start < 0 || p.End == "" {
		return "", false
	}
	ends := []int{nth(text, p.End, p.EndIndex, 0), nth(text, p.End, p.EndIndex, start+len(p.Start))}
	for _, end := range ends {
		if end < start {
			continue
		}
		candidate := text[start : end+len(p.End)]
		if p.MD5 == "" || strings.EqualFold(p.MD5, fmt.Sprintf("%x", md5.Sum([]byte(candidate)))) {
			return candidate, true
		}
	}
	return "", false
}
