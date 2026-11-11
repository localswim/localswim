package v2

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildStatusCandidateLifecycle(t *testing.T) {
	root := t.TempDir()
	before := time.Now().UnixMilli()
	status := newBuildStatus(root)
	after := time.Now().UnixMilli()
	running := status.service()
	rawID, err := hex.DecodeString(strings.ReplaceAll(running.ServiceInstanceID, "-", ""))
	if err != nil || len(rawID) != 16 {
		t.Fatalf("service_instance_id = %q", running.ServiceInstanceID)
	}
	canonicalID := fmt.Sprintf("%x-%x-%x-%x-%x", rawID[:4], rawID[4:6], rawID[6:8], rawID[8:10], rawID[10:])
	if running.ServiceInstanceID != canonicalID || rawID[6]>>4 != 7 || rawID[8]>>6 != 2 {
		t.Fatalf("service_instance_id is not canonical UUIDv7: %q", running.ServiceInstanceID)
	}
	createdAt := int64(binary.BigEndian.Uint64(rawID[:8]) >> 16)
	if createdAt < before || createdAt > after {
		t.Fatalf("service instance timestamp = %d, want [%d, %d]", createdAt, before, after)
	}
	if _, ok := normalizeID(running.BuildID); !ok {
		t.Fatalf("build_id = %q", running.BuildID)
	}
	if running.State != "running" || running.APIVersion != Version ||
		running.RestartErrorCode != nil || running.entityTag() == "" {
		t.Fatalf("running service = %+v", running)
	}
	if got := newBuildStatus(root).service(); got.ServiceInstanceID == running.ServiceInstanceID ||
		got.BuildID != running.BuildID || got.entityTag() == running.entityTag() {
		t.Fatalf("second service instance = %+v", got)
	}
	if candidates := status.builds(); candidates == nil || len(candidates) != 0 {
		t.Fatalf("empty candidate list = %#v", candidates)
	}

	artifactPath := filepath.Join(root, buildArtifactName())
	manifestPath := filepath.Join(root, buildManifestName)
	writeBuildTestFile(t, artifactPath, []byte("foreign bytes"))
	assertInvalidBuild(t, status, "candidate_manifest_missing")
	writeBuildTestFile(t, manifestPath, []byte(`{"version":"invalid","extra":true}`))
	assertInvalidBuild(t, status, "candidate_manifest_invalid")
	digest := sha256.Sum256([]byte("foreign bytes"))
	writeBuildTestFile(t, manifestPath, []byte(fmt.Sprintf(`{"version":"v2-test","sha256":"%x"}`, digest)))
	incompatible := assertInvalidBuild(t, status, "candidate_binary_incompatible")
	writeBuildTestFile(t, artifactPath, []byte("changed bytes"))
	mismatch := assertInvalidBuild(t, status, "candidate_binary_digest_mismatch")
	if mismatch.ID != incompatible.ID {
		t.Fatalf("staged ID changed with validation: %q -> %q", incompatible.ID, mismatch.ID)
	}
	if err := os.Truncate(artifactPath, buildArtifactLimit+1); err != nil {
		t.Fatal(err)
	}
	assertInvalidBuild(t, status, "candidate_binary_unreadable")
	if err := os.Remove(artifactPath); err != nil {
		t.Fatal(err)
	}
	assertInvalidBuild(t, status, "candidate_binary_missing")
	if got := status.service(); got != running {
		t.Fatalf("candidate staging changed running state: %+v -> %+v", running, got)
	}
}

func TestBuildManifestRejectsAmbiguousAndUnsafeFields(t *testing.T) {
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cases := []string{
		`{"version":"first","version":"second","sha256":"` + digest + `"}`,
		`{"version":"normal","sha256":"` + digest + `","path":"candidate.exe"}`,
		`{"version":"../private","sha256":"` + digest + `"}`,
		`{"version":null,"sha256":"` + digest + `"}`,
		`{"version":"normal","sha256":"` + strings.ToUpper(digest) + `"}`,
		`{"version":"normal","sha256":"` + digest + `"}{}`,
	}
	for _, raw := range cases {
		if _, _, valid := parseBuildManifest([]byte(raw)); valid {
			t.Errorf("accepted manifest %q", raw)
		}
	}
}

func TestBuildStatusRefusesSymlinkAndOversizedManifest(t *testing.T) {
	root := t.TempDir()
	status := newBuildStatus(root)
	manifestPath := filepath.Join(root, buildManifestName)
	writeBuildTestFile(t, manifestPath, make([]byte, buildManifestLimit+1))
	assertInvalidBuild(t, status, "candidate_manifest_unreadable")
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(root, "target.json")
	writeBuildTestFile(t, target, []byte("{}"))
	if err := os.Symlink(target, manifestPath); err != nil {
		t.Skipf("creating symlinks is unavailable: %v", err)
	}
	assertInvalidBuild(t, status, "candidate_manifest_unreadable")
}

func TestBuildStatusValidatesNativeGoBuildWithoutExecuting(t *testing.T) {
	root := t.TempDir()
	status := newBuildStatus(root)
	project := t.TempDir()
	commandPath := filepath.Join(project, "cmd", "localswim")
	if err := os.MkdirAll(commandPath, 0700); err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, filepath.Join(project, "go.mod"), []byte("module "+buildModule+"\n"))
	// This program must never execute as a side effect of validation.
	writeBuildTestFile(t, filepath.Join(commandPath, "main.go"), []byte("package main\nfunc main() { panic(\"candidate was executed\") }\n"))
	artifactPath := filepath.Join(root, buildArtifactName())
	goName := "go"
	if runtime.GOOS == "windows" {
		goName += ".exe"
	}
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", goName), "build", "-o", artifactPath, "./cmd/localswim")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building native fixture: %v\n%s", err, output)
	}
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	writeBuildTestFile(t, filepath.Join(root, buildManifestName), []byte(fmt.Sprintf(`{"version":"v2-test","sha256":"%x"}`, digest)))
	first := status.builds()
	if len(first) != 1 || first[0].Validation != "valid" || first[0].ErrorCode != nil || first[0].Version != "v2-test" {
		t.Fatalf("native candidate = %+v", first)
	}
	if _, ok := normalizeID(first[0].ID); !ok {
		t.Fatalf("candidate ID = %q", first[0].ID)
	}
	second := status.builds()
	if len(second) != 1 || second[0] != first[0] {
		t.Fatalf("candidate changed without a staging change: %+v -> %+v", first, second)
	}

	image, err := status.activationImage(first[0].ID)
	if err != nil || !bytes.Equal(image.bytes, data) || image.digest != fmt.Sprintf("%x", digest) {
		t.Fatal("activation did not retain the exact validated image")
	}
	replacement := newBuildStatus(root)
	replacement.running.BuildID = first[0].ID
	replacement.activeVersion, replacement.activeDigest = image.version, image.digest
	sameStage := replacement.builds()
	if len(sameStage) != 1 || sameStage[0] != first[0] {
		t.Fatal("replacement changed the ID of the matching staged build")
	}
	if _, err := replacement.activationImage(first[0].ID); !errors.Is(err, errActivationBuild) {
		t.Fatal("replacement accepted reactivating its running staged image")
	}

	// Preserve the executable's Go metadata and update the manifest to match
	// corrupted bytes. A matching digest must not conceal a broken entry point.
	broken := append([]byte(nil), data...)
	switch runtime.GOOS {
	case "windows":
		peOffset := binary.LittleEndian.Uint32(broken[60:])
		clear(broken[peOffset+40 : peOffset+44]) // OptionalHeader.AddressOfEntryPoint.
	case "darwin":
		clear(broken[16:20]) // Mach-O load command count.
	default:
		entryEnd := 28
		if broken[4] == 2 { // ELFCLASS64.
			entryEnd = 32
		}
		clear(broken[24:entryEnd])
	}
	writeBuildTestFile(t, artifactPath, broken)
	brokenDigest := sha256.Sum256(broken)
	writeBuildTestFile(t, filepath.Join(root, buildManifestName), []byte(fmt.Sprintf(`{"version":"v2-test","sha256":"%x"}`, brokenDigest)))
	assertInvalidBuild(t, status, "candidate_binary_incompatible")
}

func assertInvalidBuild(t *testing.T, status *buildStatus, code string) build {
	t.Helper()
	got := status.builds()
	if len(got) != 1 || got[0].Validation != "invalid" || got[0].ErrorCode == nil || *got[0].ErrorCode != code {
		t.Fatalf("candidate = %+v, want invalid / %s", got, code)
	}
	return got[0]
}

func writeBuildTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
