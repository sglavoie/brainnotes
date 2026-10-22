package buildinfo

import (
	"runtime/debug"
	"testing"
)

func settings(pairs ...string) func() (*debug.BuildInfo, bool) {
	info := &debug.BuildInfo{}
	for i := 0; i+1 < len(pairs); i += 2 {
		info.Settings = append(info.Settings, debug.BuildSetting{Key: pairs[i], Value: pairs[i+1]})
	}
	return func() (*debug.BuildInfo, bool) { return info, true }
}

func TestResolvePrefersTheLinkerOverride(t *testing.T) {
	if got := resolve("release-7", settings("vcs.revision", "abc")); got != "release-7" {
		t.Fatalf("resolve = %q, want the override", got)
	}
}

func TestResolveReadsTheStampedRevision(t *testing.T) {
	rev := "0123456789abcdef0123456789abcdef01234567"
	if got := resolve("", settings("vcs.revision", rev, "vcs.modified", "false")); got != rev {
		t.Fatalf("clean build = %q", got)
	}
	if got := resolve("", settings("vcs.revision", rev, "vcs.modified", "true")); got != rev+"-dirty" {
		t.Fatalf("dirty build = %q", got)
	}
	if got := Short(rev + "-dirty"); got != "0123456789ab-dirty" {
		t.Fatalf("Short = %q", got)
	}
}

func TestResolveWithoutARevisionIsUnknown(t *testing.T) {
	if got := resolve("", settings()); got != Unknown {
		t.Fatalf("no revision = %q", got)
	}
	if got := resolve("", func() (*debug.BuildInfo, bool) { return nil, false }); got != Unknown {
		t.Fatalf("no build info = %q", got)
	}
}

func TestMismatchNeedsTwoKnownDifferentBuilds(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"aaa", "bbb", true},
		{"aaa", "aaa", false},
		{"aaa", Unknown, false},
		{"", "bbb", false},
		{Unknown, Unknown, false},
	} {
		if got := Mismatch(tc.a, tc.b); got != tc.want {
			t.Errorf("Mismatch(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestShortKeepsAReleaseTagWhole(t *testing.T) {
	if got := Short("v0.10.0-rc.12"); got != "v0.10.0-rc.12" {
		t.Fatalf("Short = %q", got)
	}
}
