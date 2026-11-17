package v2

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// gitBlobID decides which captured files skip Git entirely, so it MUST agree
// with Git's own object IDs in both object formats.
func TestGitBlobIDMatchesGitHashObject(t *testing.T) {
	samples := [][]byte{nil, []byte("x\n"), bytes.Repeat([]byte{0, 1, 2, 255}, 5000)}
	for _, format := range []string{"sha1", "sha256"} {
		dir := t.TempDir()
		m34Git(t, dir, "init", "--object-format="+format)
		for _, data := range samples {
			command := exec.Command("git", "hash-object", "--stdin")
			command.Dir = dir
			command.Stdin = bytes.NewReader(data)
			out, err := command.Output()
			if err != nil {
				t.Fatal(err)
			}
			got, err := gitBlobID(format, data)
			if err != nil || got != strings.TrimSpace(string(out)) {
				t.Fatalf("%s blob of %d bytes: got %q, Git says %q (%v)", format, len(data), got, out, err)
			}
		}
	}
	if _, err := gitBlobID("md5", nil); err == nil {
		t.Fatal("an unknown object format was accepted")
	}
}

func TestPathConflictFindsFileDirectoryCollisions(t *testing.T) {
	entry := "100644 x 0"
	for _, check := range []struct {
		name                string
		inherited, captured []string
		conflict            bool
	}{
		{"disjoint", []string{"boards/a.json"}, []string{"boards/b.json", "attachments/c"}, false},
		{"same file", []string{"boards/a.json"}, []string{"boards/a.json"}, false},
		{"under inherited file", []string{"boards"}, []string{"boards/a.json"}, true},
		{"over inherited directory", []string{"boards/a.json/x"}, []string{"boards/a.json"}, true},
		{"captured pair", nil, []string{"attachments/a", "attachments/a/b"}, true},
	} {
		inherited, captured := map[string]string{}, map[string]string{}
		for _, name := range check.inherited {
			inherited[name] = entry
		}
		for _, name := range check.captured {
			captured[name] = entry
		}
		if got := pathConflict(inherited, captured); got != check.conflict {
			t.Errorf("%s: conflict=%t, want %t", check.name, got, check.conflict)
		}
	}
}
