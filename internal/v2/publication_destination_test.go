package v2

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func m89Publish(t *testing.T, fixture m34Fixture, fields string) ([]byte, publication) {
	t.Helper()
	body := fmt.Sprintf("{\"board_id\":%q,\"backup_id\":%q%s}", fixture.state.Board.ID, fixture.item.ID, fields)
	raw, _ := fixture.editor.call("POST", "/publications", body, "", 202)
	item := turnOneDecode[publication](t, raw)
	readback, _ := fixture.editor.call("GET", "/publications/"+item.ID, "", "", 200)
	if !bytes.Equal(raw, readback) {
		t.Fatal("publication receipt changed on readback")
	}
	return raw, item
}

func TestM89PublishesOnlyToIntendedNonOriginBranch(t *testing.T) {
	for _, mode := range []string{"upstream", "refspec", "explicit"} {
		t.Run(mode, func(t *testing.T) {
			fixture := m34PublicationFixture(t)
			repo, target := fixture.repository, t.TempDir()
			m34Git(t, target, "init", "--bare", "--initial-branch=delivery")
			// Deliberately use a different fetch URL. The selected push URL
			// is either explicit or computed by Git's pushInsteadOf rules.
			fetch := fixture.remote
			if mode == "refspec" {
				fetch = "m89-disposable:"
				m34Git(t, repo, "config", "url."+target+".pushInsteadOf", fetch)
			}
			m34Git(t, repo, "remote", "add", "publish", fetch)
			if mode != "refspec" {
				m34Git(t, repo, "config", "remote.publish.pushurl", target)
			}
			var fields string
			switch mode {
			case "upstream":
				m34Git(t, repo, "config", "branch.main.remote", "publish")
				m34Git(t, repo, "config", "branch.main.merge", "refs/heads/delivery")
				m34Git(t, repo, "config", "push.default", "upstream")
			case "refspec":
				m34Git(t, repo, "config", "branch.main.pushRemote", "publish")
				m34Git(t, repo, "config", "remote.pushDefault", "origin")
				m34Git(t, repo, "config", "remote.publish.push", "HEAD:refs/heads/delivery")
			case "explicit":
				m34Git(t, repo, "config", "branch.main.pushRemote", "origin")
				m34Git(t, repo, "config", "remote.publish.push", "refs/heads/*:refs/heads/*")
				fields = ",\"remote\":\"publish\",\"branch\":\"delivery\""
			}
			before := m34Git(t, repo, "rev-parse", "HEAD")
			_, receipt := m89Publish(t, fixture, fields)
			if receipt.State != "succeeded" || receipt.ErrorCode != nil {
				t.Fatalf("publication failed: %s", snapshotString(receipt.ErrorCode))
			}
			head := m34Git(t, repo, "rev-parse", "refs/heads/main")
			if bytes.Equal(before, head) ||
				!bytes.Equal(head, m34Git(t, target, "rev-parse", "refs/heads/delivery")) ||
				!bytes.Equal(before, m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")) {
				t.Fatal("publication did not advance only the local source and selected remote branch")
			}
			if refs := string(m34Git(t, target, "for-each-ref", "--format=%(refname)")); refs != "refs/heads/delivery\n" {
				t.Fatal("publication pushed additional refs")
			}
			for name, contents := range fixture.contents {
				if !bytes.Equal(contents, m34Git(t, target, "show", "refs/heads/delivery:"+fixture.prefix+"/"+name)) {
					t.Fatal("destination differs from the verified capture")
				}
			}
		})
	}
}

func TestM89RefusesUnresolvedTargetsBeforeGitWrites(t *testing.T) {
	fixture := m34PublicationFixture(t)
	repo := fixture.repository
	m34Git(t, repo, "remote", "add", "publish", fixture.remote)
	refs := m34Git(t, repo, "for-each-ref", "--format=%(refname) %(objectname)")
	remoteRefs := m34Git(t, fixture.remote, "for-each-ref", "--format=%(refname) %(objectname)")
	objects := m34Git(t, repo, "count-objects", "-v")
	for _, test := range []struct {
		name, fields, code string
		config             [][2]string
	}{
		{name: "unselected remote", code: "publication_target_ambiguous"},
		{name: "multiple push URLs", fields: ",\"remote\":\"publish\"", code: "publication_target_ambiguous",
			config: [][2]string{{"remote.publish.pushurl", fixture.remote}, {"remote.publish.pushurl", t.TempDir()}}},
		{name: "multiple refspecs", fields: ",\"remote\":\"publish\"", code: "publication_target_ambiguous",
			config: [][2]string{{"remote.publish.push", "HEAD:refs/heads/one"}, {"remote.publish.push", "HEAD:refs/heads/two"}}},
		{name: "matching", fields: ",\"remote\":\"publish\"", code: "publication_target_ambiguous",
			config: [][2]string{{"push.default", "matching"}}},
		{name: "simple with different upstream", fields: ",\"remote\":\"publish\"", code: "publication_destination_unavailable",
			config: [][2]string{{"push.default", "simple"}, {"branch.main.remote", "publish"}, {"branch.main.merge", "refs/heads/different"}}},
		{name: "unsupported transport", fields: ",\"remote\":\"publish\",\"branch\":\"delivery\"", code: "unsupported_remote_transport",
			config: [][2]string{{"remote.publish.proxyAuthMethod", ""}}},
		{name: "nonbranch ref", fields: ",\"remote\":\"publish\",\"branch\":\"refs/tags/example\"", code: "publication_destination_unavailable"},
		{name: "unknown remote", fields: ",\"remote\":\"missing\",\"branch\":\"delivery\"", code: "remote_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			keys := map[string]bool{}
			for _, pair := range test.config {
				m34Git(t, repo, "config", "--add", pair[0], pair[1])
				keys[pair[0]] = true
			}
			t.Cleanup(func() {
				for key := range keys {
					m34Git(t, repo, "config", "--unset-all", key)
				}
			})
			_, receipt := m89Publish(t, fixture, test.fields)
			if receipt.State != "failed" || snapshotString(receipt.ErrorCode) != test.code {
				t.Fatalf("publication returned %q, want %q", snapshotString(receipt.ErrorCode), test.code)
			}
			if !bytes.Equal(refs, m34Git(t, repo, "for-each-ref", "--format=%(refname) %(objectname)")) ||
				!bytes.Equal(remoteRefs, m34Git(t, fixture.remote, "for-each-ref", "--format=%(refname) %(objectname)")) ||
				!bytes.Equal(objects, m34Git(t, repo, "count-objects", "-v")) {
				t.Fatal("refused destination wrote Git objects or changed refs")
			}
		})
	}
}

func TestM89CredentialBearingTransportFailureHasOpaqueReceipt(t *testing.T) {
	fixture := m34PublicationFixture(t)
	repo := fixture.repository
	const user, marker = "fixture-account", "fixture-credential"
	m34Git(t, repo, "remote", "add", "blocked", fixture.remote)
	m34Git(t, repo, "config", "remote.blocked.pushurl", "https://"+user+":"+marker+"@example.invalid/disposable")
	// Git refuses the protocol before attempting any network or credential
	// helper call. Exercise the real diagnostic boundary with fake URL data.
	m34Git(t, repo, "config", "protocol.https.allow", "never")
	raw, receipt := m89Publish(t, fixture, ",\"remote\":\"blocked\",\"branch\":\"delivery\"")
	if receipt.State != "failed" || snapshotString(receipt.ErrorCode) != "git_push_failed" ||
		strings.Contains(string(raw), user) || strings.Contains(string(raw), marker) ||
		strings.Contains(string(raw), "example.invalid") {
		t.Fatal("transport refusal did not return a fixed, opaque error")
	}
}
