package quotes

import (
	"crypto/md5"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"path/filepath"
	"testing"
	"time"
)

func TestPersistenceScopeAndExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quotes.json")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Put("bot", "peer", "18446744073709551615", Content{Text: "原话"}); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	if got, ok := reopened.Get("bot", "peer", "18446744073709551615"); !ok || got.Text != "原话" {
		t.Fatal("missing persisted quote")
	}
	for _, scope := range [][2]string{{"other", "peer"}, {"bot", "other"}} {
		if _, ok := reopened.Get(scope[0], scope[1], "18446744073709551615"); ok {
			t.Fatal("cross-scope quote leak")
		}
	}
	reopened.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	if _, ok := reopened.Get("bot", "peer", "18446744073709551615"); ok {
		t.Fatal("expired quote returned")
	}
}

func TestPartialSelectsVerifiedOccurrence(t *testing.T) {
	full := "甲头一尾，甲头二尾，甲头三尾"
	p := &weixin.PartialText{Start: "头", End: "尾", StartIndex: 1, EndIndex: 1}
	if text, ok := Partial(full, p); !ok || text != "头二尾" {
		t.Fatal(text, ok)
	}
	p.EndIndex = 0
	p.MD5 = fmt.Sprintf("%x", md5.Sum([]byte("头二尾")))
	if text, ok := Partial(full, p); !ok || text != "头二尾" {
		t.Fatal("relative end variant", text, ok)
	}
	p.MD5 = "00000000000000000000000000000000"
	if _, ok := Partial(full, p); ok {
		t.Fatal("invalid selection hash accepted")
	}
	p.StartIndex = -1
	if _, ok := Partial(full, p); ok {
		t.Fatal("negative occurrence accepted")
	}
}
