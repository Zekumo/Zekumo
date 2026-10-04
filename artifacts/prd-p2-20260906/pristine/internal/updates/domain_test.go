package updates

import (
	"testing"

	"minicloud/internal/repo"
)

func published(id int64, version string, rollout int) repo.Release {
	return repo.Release{ID: id, Version: version, Status: "published", RolloutPercent: rollout}
}

func TestPickReleaseNewestFullRollout(t *testing.T) {
	releases := []repo.Release{
		published(1, "1.0.0", 100),
		published(3, "1.2.0", 100),
		published(2, "1.1.0", 100),
	}
	got := pickRelease(releases, "")
	if got == nil || got.Version != "1.2.0" {
		t.Fatalf("want 1.2.0, got %+v", got)
	}
}

func TestPickReleasePartialRolloutFallsBack(t *testing.T) {
	releases := []repo.Release{
		published(1, "1.0.0", 100),
		published(2, "2.0.0", 0), // rolled out to nobody
	}
	got := pickRelease(releases, "some-device")
	if got == nil || got.Version != "1.0.0" {
		t.Fatalf("want fallback to 1.0.0, got %+v", got)
	}
	// Without a device_id, partial rollouts never match.
	got = pickRelease(releases, "")
	if got == nil || got.Version != "1.0.0" {
		t.Fatalf("want 1.0.0 for empty device, got %+v", got)
	}
}

func TestPickReleaseRolloutDeterministic(t *testing.T) {
	releases := []repo.Release{
		published(1, "1.0.0", 100),
		published(2, "2.0.0", 50),
	}
	first := pickRelease(releases, "device-a").Version
	for i := 0; i < 10; i++ {
		if v := pickRelease(releases, "device-a").Version; v != first {
			t.Fatalf("selection not deterministic: %s then %s", first, v)
		}
	}
}

func TestPickReleaseEmpty(t *testing.T) {
	if got := pickRelease(nil, "x"); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"1.1.0", "1.0.0", true},
		{"1.0.0", "1.0.0", false},
		{"0.9.0", "1.0.0", false},
		{"2.0.0-beta.1", "1.9.9", true},
		{"1.0.0", "garbage", true},  // unknown client build → offer latest
		{"garbage", "1.0.0", false}, // unparseable candidate → never offer
	}
	for _, c := range cases {
		if got := isNewer(c.candidate, c.current); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.candidate, c.current, got, c.want)
		}
	}
}

func TestIsMandatory(t *testing.T) {
	r := repo.Release{Mandatory: false, MinSupportedVersion: "1.5.0"}
	if !isMandatory(&r, "1.4.0") {
		t.Error("client below min_supported_version should be forced")
	}
	if isMandatory(&r, "1.5.0") {
		t.Error("client at min_supported_version should not be forced")
	}
	forced := repo.Release{Mandatory: true}
	if !isMandatory(&forced, "99.0.0") {
		t.Error("mandatory release should always force")
	}
}

func TestPickArtifact(t *testing.T) {
	ready := func(platform, arch string) repo.Artifact {
		return repo.Artifact{Platform: platform, Arch: arch, Status: "ready"}
	}
	artifacts := []repo.Artifact{
		ready("windows", "amd64"),
		ready("windows", "any"),
		ready("any", "any"),
		{Platform: "linux", Arch: "amd64", Status: "pending"},
	}
	if got := pickArtifact(artifacts, "windows", "amd64"); got == nil || got.Arch != "amd64" {
		t.Errorf("want exact windows/amd64 match, got %+v", got)
	}
	if got := pickArtifact(artifacts, "windows", "arm64"); got == nil || got.Arch != "any" {
		t.Errorf("want windows/any fallback, got %+v", got)
	}
	if got := pickArtifact(artifacts, "macos", "arm64"); got == nil || got.Platform != "any" {
		t.Errorf("want any/any fallback, got %+v", got)
	}
	if got := pickArtifact(artifacts, "linux", "amd64"); got == nil || got.Platform != "any" {
		t.Errorf("pending artifact must be skipped, got %+v", got)
	}
}
