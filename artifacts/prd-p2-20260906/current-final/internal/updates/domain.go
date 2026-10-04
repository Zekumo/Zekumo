// Package updates implements the app-release and update-distribution
// subsystem: developers publish versioned artifacts per game, game clients
// poll a public endpoint to discover and download updates.
package updates

import (
	"hash/fnv"

	"github.com/Masterminds/semver/v3"
	"minicloud/internal/repo"
)

var ValidPlatforms = map[string]bool{
	"windows": true, "macos": true, "linux": true,
	"android": true, "ios": true, "web": true, "any": true,
}

var ValidArchs = map[string]bool{"amd64": true, "arm64": true, "any": true}

// pickRelease selects the release an update-checking client should receive.
// releases must contain only published releases of one game+channel; they are
// sorted newest-first by semver before selection. A partially rolled-out
// release only applies to clients whose device hash falls inside the
// percentage; everyone else falls through to the next fully rolled-out one.
func pickRelease(releases []repo.Release, deviceID string) *repo.Release {
	sortBySemverDesc(releases)
	for i := range releases {
		r := &releases[i]
		if r.RolloutPercent >= 100 {
			return r
		}
		if deviceID != "" && rolloutBucket(deviceID, r.ID) < r.RolloutPercent {
			return r
		}
	}
	return nil
}

// isNewer reports whether candidate is a strictly newer semver than current.
// Unparseable versions are treated conservatively: candidate unparseable →
// false; current unparseable → true (client is on an unknown build, offer it
// the latest).
func isNewer(candidate, current string) bool {
	cv, err := semver.NewVersion(candidate)
	if err != nil {
		return false
	}
	cur, err := semver.NewVersion(current)
	if err != nil {
		return true
	}
	return cv.GreaterThan(cur)
}

// isMandatory reports whether the update must be forced for a client on
// currentVersion.
func isMandatory(r *repo.Release, currentVersion string) bool {
	if r.Mandatory {
		return true
	}
	if r.MinSupportedVersion == "" {
		return false
	}
	minV, err := semver.NewVersion(r.MinSupportedVersion)
	if err != nil {
		return false
	}
	cur, err := semver.NewVersion(currentVersion)
	if err != nil {
		return true
	}
	return cur.LessThan(minV)
}

// pickArtifact chooses the artifact matching the client's platform/arch,
// preferring exact matches and falling back through "any".
func pickArtifact(artifacts []repo.Artifact, platform, arch string) *repo.Artifact {
	var best *repo.Artifact
	bestScore := -1
	for i := range artifacts {
		a := &artifacts[i]
		if a.Status != "ready" {
			continue
		}
		p, ar := matchScore(a.Platform, platform), matchScore(a.Arch, arch)
		if p < 0 || ar < 0 {
			continue
		}
		if score := p*2 + ar; score > bestScore {
			best, bestScore = a, score
		}
	}
	return best
}

// matchScore: exact match 1, "any" wildcard 0, mismatch -1.
func matchScore(have, want string) int {
	switch {
	case have == want:
		return 1
	case have == "any":
		return 0
	default:
		return -1
	}
}

// rolloutBucket maps a device deterministically into [0, 100) per release, so
// a device stays in or out of a rollout as the percentage only grows.
func rolloutBucket(deviceID string, releaseID int64) int {
	h := fnv.New32a()
	h.Write([]byte(deviceID))
	var buf [8]byte
	for i := 0; i < 8; i++ {
		buf[i] = byte(releaseID >> (8 * i))
	}
	h.Write(buf[:])
	return int(h.Sum32() % 100)
}

func sortBySemverDesc(releases []repo.Release) {
	// Insertion sort: release lists per channel are short.
	for i := 1; i < len(releases); i++ {
		for j := i; j > 0 && semverLess(releases[j-1].Version, releases[j].Version); j-- {
			releases[j-1], releases[j] = releases[j], releases[j-1]
		}
	}
}

func semverLess(a, b string) bool {
	av, errA := semver.NewVersion(a)
	bv, errB := semver.NewVersion(b)
	if errA != nil || errB != nil {
		return a < b
	}
	return av.LessThan(bv)
}
