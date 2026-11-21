package main

import (
	"runtime/debug"
	"testing"
)

func TestServerVersion(t *testing.T) {
	t.Parallel()
	built := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }

	for _, tc := range []struct {
		name     string
		injected string
		info     *debug.BuildInfo
		ok       bool
		want     string
	}{
		{"release archive", "v0.1.0", built("(devel)"), true, "v0.1.0"},
		{"go install at a tag", "", built("v0.1.0"), true, "v0.1.0"},
		{"no build info", "", nil, false, "devel"},
	} {
		if got := serverVersion(tc.injected, tc.info, tc.ok); got != tc.want {
			t.Errorf("%s: version %q, want %q", tc.name, got, tc.want)
		}
	}
}
