package scholar

import "testing"

func TestPrivateSourcesAndExternalProxyRejected(t *testing.T) {
	for _, s := range []string{"file:///etc/passwd", "https://localhost/x", "https://127.0.0.1/x", "https://10.0.0.1/x", "https://169.254.169.254/latest", "https://user:pass@example.org/x", "https://x.internal/", "https://[::1]/"} {
		if SafeURL(s) == nil {
			t.Errorf("allowed %s", s)
		}
	}
	if SafeURL("https://doi.org/10.1000/example") != nil {
		t.Fatal("public DOI rejected")
	}
	if _, e := New("socks5://127.0.0.1:19923", Keys{}); e != nil {
		t.Fatal(e)
	}
	if _, e := New("socks5://8.8.8.8:1080", Keys{}); e == nil {
		t.Fatal("external proxy accepted")
	}
}
