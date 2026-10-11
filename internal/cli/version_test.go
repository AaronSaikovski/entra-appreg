package cli

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersionPrefersLinkedTagThenBuildInfoThenDev(t *testing.T) {
	stamped := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Version: v}}, true
		}
	}
	missing := func() (*debug.BuildInfo, bool) { return nil, false }
	for _, c := range []struct {
		name, linked string
		info         func() (*debug.BuildInfo, bool)
		want         string
	}{
		{"linked tag wins and loses its v", "v0.0.1", stamped("v9.9.9"), "0.0.1"},
		{"linked value without v is kept", "0.2.0-rc.1", stamped("v9.9.9"), "0.2.0-rc.1"},
		{"go install tag", "", stamped("v0.0.1"), "0.0.1"},
		{"VCS pseudo-version", "", stamped("v0.0.2-0.20261011000000-abcdef123456+dirty"), "0.0.2-0.20261011000000-abcdef123456+dirty"},
		{"unstamped devel build", "", stamped("(devel)"), "dev"},
		{"empty module version", "", stamped(""), "dev"},
		{"no build info", "", missing, "dev"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveVersion(c.linked, c.info); got != c.want {
				t.Errorf("resolveVersion(%q) = %q, want %q", c.linked, got, c.want)
			}
		})
	}
}
