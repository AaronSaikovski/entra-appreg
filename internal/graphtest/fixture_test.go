package graphtest

import (
	"io"
	"net/http"
	"testing"
)

func TestZeroStatusDefaultsToOK(t *testing.T) {
	f := New(t, Response{Body: `{"ok":true}`})
	resp, err := http.Get(f.Base + "/x")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != `{"ok":true}` {
		t.Fatalf("got %d %q, want 200 {\"ok\":true}", resp.StatusCode, body)
	}
}
