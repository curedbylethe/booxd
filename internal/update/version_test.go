package update

import "testing"

func TestIsRelease(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
		want    bool
	}{
		{"release", "v1.2.3", true},
		{"prerelease", "v1.2.3-rc1", true},
		{"empty", "", false},
		{"dev", "dev", false},
		{"no leading v", "1.2.3", false},
		{"two components", "v1.2", false},
		{"four components", "v1.2.3.4", false},
		{"non numeric", "v1.x.3", false},
		{"prefix only", "v", false},
		{"double prefix", "vv1.2.3", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRelease(tc.version); got != tc.want {
				t.Fatalf("IsRelease(%q) = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"patch", "v1.2.3", "v1.2.4", true},
		{"minor", "v1.2.3", "v1.3.0", true},
		{"major", "v1.2.3", "v2.0.0", true},
		{"equal", "v1.2.3", "v1.2.3", false},
		{"older", "v1.2.4", "v1.2.3", false},
		{"current dev", "dev", "v1.2.4", false},
		{"latest dev", "v1.2.3", "dev", false},
		{"malformed current", "v1.2", "v1.2.4", false},
		{"malformed latest", "v1.2.3", "", false},
		{"prerelease current", "v1.2.3-rc1", "v1.2.4", true},
		{"prerelease ignored", "v1.2.3-rc1", "v1.2.3", false},
		{"build suffix ignored", "v1.2.3+build1", "v1.2.3", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Newer(tc.current, tc.latest); got != tc.want {
				t.Fatalf("Newer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}
