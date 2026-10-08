package files

import (
	"fmt"
	"testing"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
)

func TestGrantCapacityReopenAndExpiryPreserveDevices(t *testing.T) {
	defer metadb.CloseAll()
	root := t.TempDir()
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	device, e := s.Grant("owner", "device", 30*24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	for i := 1; i < MaxGrants; i++ {
		if _, e = s.Grant("owner", fmt.Sprintf("link-%d", i)); e != nil {
			t.Fatal("links exhausted prematurely", i, e)
		}
	}
	if _, e = s.Grant("owner", "overflow"); e == nil {
		t.Fatal("unbounded grants")
	}
	s, e = Open(root)
	if e != nil {
		t.Fatal("valid grants rejected on restart", e)
	}
	if owner, ok := s.Authorize(device); !ok || owner != "owner" {
		t.Fatal("existing device lost")
	}
	now := time.Now().Add(31 * time.Minute)
	s.now = func() time.Time { return now }
	if _, e = s.Grant("owner", "new-link"); e != nil || len(s.state.Grants) != 2 {
		t.Fatal("expired links not retired", len(s.state.Grants), e)
	}
	if _, ok := s.Authorize(device); !ok {
		t.Fatal("device revoked with short links")
	}
}
