package rail0

import (
	"regexp"
	"testing"
)

// The release workflow tags "v" + Version and Go resolves the module by that tag, so a
// Version that is not plain semver X.Y.Z would produce a tag no `go get` can use.
func TestVersionIsSemver(t *testing.T) {
	semver := regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	if !semver.MatchString(Version) {
		t.Fatalf("Version = %q, want X.Y.Z", Version)
	}
}
