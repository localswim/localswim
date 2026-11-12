package v2

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const m97DestinationRef = "refs/heads/delivery"

func m97PublicationTarget(t *testing.T, fixture m34Fixture) (string, []byte) {
	t.Helper()
	remote := t.TempDir()
	m34Git(t, remote, "init", "--bare", "--initial-branch=delivery")
	m34Git(t, fixture.repository, "remote", "add", "delivery", remote)
	m34Git(t, fixture.repository, "push", "delivery", "HEAD:"+m97DestinationRef)
	return remote, m34Git(t, remote, "rev-parse", m97DestinationRef)
}

func m97AssertRemoteCommit(t *testing.T, remote string, baseline, commit []byte) {
	t.Helper()
	if tip := m34Git(t, remote, "rev-parse", m97DestinationRef); !bytes.Equal(tip, commit) {
		t.Fatal("the original destination did not receive the pending commit")
	}
	if refs := m34Git(t, remote, "for-each-ref", "--format=%(refname)"); string(refs) != m97DestinationRef+"\n" {
		t.Fatal("publication created an extra destination ref")
	}
	if count := m34Git(t, remote, "rev-list", "--count", strings.TrimSpace(string(baseline))+".."+m97DestinationRef); string(count) != "1\n" {
		t.Fatal("the original destination received more than one commit")
	}
}

func TestM97AutomaticPendingPushRefusesChangedGitDefaultAfterRestart(t *testing.T) {
	fixture := m34PublicationFixture(t)
	remote, baseline := m97PublicationTarget(t, fixture)
	repo := fixture.repository
	m34Git(t, repo, "config", "branch.main.remote", "delivery")
	m34Git(t, repo, "config", "branch.main.merge", m97DestinationRef)
	m34Git(t, repo, "config", "push.default", "upstream")
	offline := remote + "-offline"
	if err := os.Rename(remote, offline); err != nil {
		t.Fatal(err)
	}

	path := "/boards/" + fixture.state.Board.ID
	fixture.editor.call("PATCH", path, `{"publishing":{"enabled":true}}`, fixture.state.Board.entityTag(), 200)
	failed := waitBoardPublishing(t, fixture.editor, path, func(info publishing) bool {
		return info.Pending && snapshotString(info.ErrorCode) == "git_push_failed"
	})
	pending := m34Git(t, repo, "rev-parse", "HEAD")
	if bytes.Equal(pending, baseline) {
		t.Fatal("automatic publication did not make its local commit")
	}
	if err := fixture.app.Close(); err != nil {
		t.Fatal(err)
	}
	root := fixture.app.store.root
	stateRef := publicationCheckpointRef(root, failed.ID)
	intent := m34Git(t, repo, "rev-parse", stateRef)
	m34Git(t, repo, "config", "push.default", "current")
	if err := os.Rename(offline, remote); err != nil {
		t.Fatal(err)
	}
	_, restarted := restartBoardPublication(t, root, fixture.editor.token)
	refused := waitBoardPublishing(t, restarted, path, func(info publishing) bool {
		return info.Pending && snapshotString(info.ErrorCode) == "publication_destination_changed"
	})
	if refused.entityTag() != failed.entityTag() || refused.Publishing.LastSuccessAt != nil ||
		!bytes.Equal(intent, m34Git(t, repo, "rev-parse", stateRef)) ||
		!bytes.Equal(pending, m34Git(t, repo, "rev-parse", "HEAD")) {
		t.Fatal("destination refusal changed the checkpoint, commit, or revision")
	}
	if refs := m34Git(t, remote, "for-each-ref", "--format=%(refname)"); string(refs) != m97DestinationRef+"\n" ||
		!bytes.Equal(baseline, m34Git(t, remote, "rev-parse", m97DestinationRef)) {
		t.Fatal("a retry under changed defaults moved a remote ref")
	}

	m34Git(t, repo, "config", "push.default", "upstream")
	waitBoardPublishing(t, restarted, path, func(info publishing) bool {
		return !info.Pending && info.ErrorCode == nil && info.LastSuccessAt != nil
	})
	m97AssertRemoteCommit(t, remote, baseline, pending)
	if !bytes.Equal(pending, m34Git(t, repo, "rev-parse", "HEAD")) ||
		!bytes.Equal(baseline, m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")) {
		t.Fatal("recovery recreated the commit or changed origin")
	}
}

func TestM97ManualRetryKeepsRemoteIdentityAndOriginalCommit(t *testing.T) {
	fixture := m34PublicationFixture(t)
	remote, baseline := m97PublicationTarget(t, fixture)
	repo := fixture.repository
	const fields = `,"remote":"delivery","branch":"delivery"`
	offline := remote + "-offline"
	if err := os.Rename(remote, offline); err != nil {
		t.Fatal(err)
	}
	_, failed := m89Publish(t, fixture, fields)
	if failed.State != "failed" || snapshotString(failed.ErrorCode) != "git_push_failed" {
		t.Fatal("the interrupted manual push did not fail durably")
	}
	pending := m34Git(t, repo, "rev-parse", "HEAD")
	if bytes.Equal(pending, baseline) {
		t.Fatal("manual publication did not make its local commit")
	}
	stateRef := publicationCheckpointRef(fixture.app.store.root, "backup-"+fixture.item.ID)
	intent := m34Git(t, repo, "rev-parse", stateRef)
	if journal := m34Git(t, repo, "cat-file", "commit", stateRef); bytes.Contains(journal, []byte(remote)) {
		t.Fatal("the pending checkpoint stored the raw push URL")
	}
	if err := os.Rename(offline, remote); err != nil {
		t.Fatal(err)
	}
	// Remote aliases can have different transport configuration even when
	// they currently resolve to the same push URL.
	m34Git(t, repo, "remote", "add", "alias", remote)
	_, changedAlias := m89Publish(t, fixture, `,"remote":"alias","branch":"delivery"`)
	if changedAlias.State != "failed" || snapshotString(changedAlias.ErrorCode) != "publication_destination_changed" {
		t.Fatal("manual retry accepted a different configured remote identity")
	}
	m34Git(t, repo, "config", "remote.delivery.pushurl", fixture.remote)
	_, changedURL := m89Publish(t, fixture, fields)
	if changedURL.State != "failed" || snapshotString(changedURL.ErrorCode) != "publication_destination_changed" ||
		!bytes.Equal(intent, m34Git(t, repo, "rev-parse", stateRef)) ||
		!bytes.Equal(baseline, m34Git(t, remote, "rev-parse", m97DestinationRef)) ||
		string(m34Git(t, fixture.remote, "for-each-ref", "--format=%(refname)")) != "refs/heads/main\n" {
		t.Fatal("changed push URL altered the checkpoint or wrote to a remote")
	}
	m34Git(t, repo, "config", "--unset", "remote.delivery.pushurl")

	// A new local commit must not be included when retrying the immutable
	// backup. It is unrelated to the accepted publication.
	m34Write(t, filepath.Join(repo, "unrelated-retry.txt"), []byte("local only\n"))
	m34Git(t, repo, "add", "--", "unrelated-retry.txt")
	m34Git(t, repo, "commit", "-m", "Unrelated later local commit")
	head := m34Git(t, repo, "rev-parse", "HEAD")
	// A hex-shaped local branch name can shadow a bare Git push refspec.
	m34Git(t, repo, "update-ref", "refs/heads/"+strings.TrimSpace(string(pending)), strings.TrimSpace(string(head)))
	// Simulate the private source remaining after interruption in Git transport.
	source := publicationSendRefPrefix + strings.TrimSpace(string(intent)) + "/commit"
	m34Git(t, repo, "update-ref", source, strings.TrimSpace(string(pending)))
	collision := source + "-unexpected"
	m34Git(t, repo, "update-ref", collision, strings.TrimSpace(string(head)))
	_, blocked := m89Publish(t, fixture, fields)
	if blocked.State != "failed" || snapshotString(blocked.ErrorCode) != "publication_state_unavailable" ||
		!bytes.Equal(head, m34Git(t, repo, "show-ref", "--verify", "--hash", collision)) ||
		!bytes.Equal(baseline, m34Git(t, remote, "show-ref", "--verify", "--hash", m97DestinationRef)) ||
		string(m34Git(t, remote, "for-each-ref", "--format=%(refname)")) != m97DestinationRef+"\n" {
		t.Fatal("an unexpected private ref was changed or included in the push")
	}
	m34Git(t, repo, "update-ref", "-d", collision, strings.TrimSpace(string(head)))
	_, recovered := m89Publish(t, fixture, fields)
	if recovered.State != "succeeded" || recovered.ErrorCode != nil || recovered.ID == failed.ID {
		t.Fatal("manual recovery did not return a successful new receipt")
	}
	m97AssertRemoteCommit(t, remote, baseline, pending)
	if !bytes.Equal(head, m34Git(t, repo, "rev-parse", "HEAD")) ||
		len(m34Git(t, repo, "for-each-ref", "--format=%(refname)", stateRef, publicationSendRefPrefix)) != 0 {
		t.Fatal("manual recovery moved the local branch or retained a settled checkpoint")
	}
}

func TestM97ManualPendingPushSurvivesInterruptedReceipt(t *testing.T) {
	fixture := m34PublicationFixture(t)
	remote, baseline := m97PublicationTarget(t, fixture)
	root := fixture.app.store.root
	original := publication{
		ID: newOperationID(), BoardID: fixture.item.BoardID, BackupID: fixture.item.ID, State: "running",
	}
	if err := savePublication(root, original, true); err != nil {
		t.Fatal(err)
	}
	selection := publicationSelection{remote: "delivery", branch: "delivery"}
	err := publishVerifiedBackup(root, fixture.item, selection, func(pushErr error) error {
		if pushErr != nil {
			t.Fatal("fixture push failed before receipt persistence", pushErr)
		}
		return errWriteUncertain
	})
	if err != errWriteUncertain {
		t.Fatal("the simulated receipt interruption did not leave an uncertain result", err)
	}
	raw, _ := fixture.editor.call("GET", "/publications/"+original.ID, "", "", 200)
	if item := turnOneDecode[publication](t, raw); item.State != "running" || item.FinishedAt != nil {
		t.Fatal("the interrupted receipt did not remain inspectable")
	}
	pending := m34Git(t, fixture.repository, "rev-parse", "HEAD")
	m97AssertRemoteCommit(t, remote, baseline, pending)

	_, redirected := m89Publish(t, fixture, `,"remote":"origin","branch":"main"`)
	if redirected.State != "failed" || snapshotString(redirected.ErrorCode) != "publication_destination_changed" ||
		!bytes.Equal(baseline, m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")) {
		t.Fatal("a new receipt could redirect the pending push")
	}
	_, recovered := m89Publish(t, fixture, `,"remote":"delivery","branch":"delivery"`)
	if recovered.State != "succeeded" || recovered.ErrorCode != nil || recovered.ID == original.ID {
		t.Fatal("the original destination did not settle under a new receipt")
	}
	m97AssertRemoteCommit(t, remote, baseline, pending)

	// A durable successful receipt releases the backup for a separate
	// publication with an intentionally different destination.
	_, separate := m89Publish(t, fixture, `,"remote":"origin","branch":"main"`)
	if separate.State != "succeeded" || !bytes.Equal(pending, m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")) {
		t.Fatal("a settled receipt still prevented deliberate publication to another destination")
	}
}

func TestM97PushCreatesExactMissingRefWithExistingGitDWIMMatch(t *testing.T) {
	fixture := m34PublicationFixture(t)
	repo := fixture.repository
	const alternateRef = "refs/heads/refs/heads/delivery"
	m34Git(t, repo, "push", "origin", "HEAD:"+alternateRef)
	baseline := m34Git(t, fixture.remote, "show-ref", "--verify", "--hash", alternateRef)
	_, outcome := m89Publish(t, fixture, `,"remote":"origin","branch":"delivery"`)
	if outcome.State != "succeeded" || outcome.ErrorCode != nil {
		t.Fatal("publication failed with a differently named remote ref present")
	}
	if got := m34Git(t, fixture.remote, "show-ref", "--verify", "--hash", alternateRef); !bytes.Equal(got, baseline) {
		t.Fatal("Git redirected the push into the alternate remote ref")
	}
	head := m34Git(t, repo, "rev-parse", "HEAD")
	if got := m34Git(t, fixture.remote, "show-ref", "--verify", "--hash", m97DestinationRef); !bytes.Equal(got, head) {
		t.Fatal("publication did not create the exact requested destination ref")
	}
	if refs := m34Git(t, repo, "for-each-ref", "--format=%(refname)", publicationSendRefPrefix); len(refs) != 0 {
		t.Fatal("publication did not clean up its private transport ref")
	}
}
