package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

type pendingPublication struct {
	Commit string `json:"commit"`
	Parent string `json:"parent"`
	Branch string `json:"branch"`
	Remote string `json:"remote"`
}

type publicationCheckpoint struct {
	Revision      uint64              `json:"revision"`
	LastSuccessAt *time.Time          `json:"last_success_at"`
	Pending       *pendingPublication `json:"pending"`
}

func publicationCheckpointRef(root, key string) string {
	digest := sha256.Sum256([]byte(root + "\x00" + key))
	return "refs/localswim/publication/" + hex.EncodeToString(digest[:])
}

func gitObjectID(text string) bool {
	return (len(text) == 40 || len(text) == 64) && validPublicationDigest(text)
}

func validPublicationDigest(text string) bool {
	_, err := hex.DecodeString(text)
	return err == nil && strings.ToLower(text) == text
}

func (repo publicationRepository) checkpoint(ref string) (publicationCheckpoint, string, error) {
	var checkpoint publicationCheckpoint
	oid, err := publicationGitText(repo.root, "for-each-ref", "--format=%(objectname)", "--", ref)
	if err != nil || (oid != "" && !gitObjectID(oid)) {
		return checkpoint, "", gitPublicationError("publication_state_unavailable")
	}
	if oid == "" {
		return checkpoint, "", nil
	}
	data, err := publicationGit(repo.root, "", nil, "cat-file", "commit", oid)
	headers, message, found := strings.Cut(string(data), "\n\n")
	if err != nil || !found || unmarshalCanonical([]byte(message), &checkpoint) != nil ||
		checkpoint.Revision == 0 || checkpoint.Revision > maxSnapshotInteger ||
		(checkpoint.LastSuccessAt != nil && !validSnapshotTime(*checkpoint.LastSuccessAt)) {
		return publicationCheckpoint{}, "", gitPublicationError("publication_state_unavailable")
	}
	if pending := checkpoint.Pending; pending != nil &&
		(!gitObjectID(pending.Commit) || !gitObjectID(pending.Parent) ||
			!strings.HasPrefix(pending.Branch, "refs/heads/") ||
			strings.ContainsAny(pending.Branch, " \r\n\t") ||
			len(pending.Remote) != 64 || !validPublicationDigest(pending.Remote) ||
			!strings.Contains(headers, "\nparent "+pending.Commit+"\n")) {
		return publicationCheckpoint{}, "", gitPublicationError("publication_state_unavailable")
	}
	return checkpoint, oid, nil
}

func (repo publicationRepository) saveCheckpoint(ref, oid string, checkpoint publicationCheckpoint) (string, error) {
	data, err := marshalCanonical(checkpoint)
	if err != nil {
		return "", gitPublicationError("publication_state_unavailable")
	}
	tree, err := publicationGitText(repo.root, "mktree")
	if err != nil || !gitObjectID(tree) {
		return "", gitPublicationError("publication_state_unavailable")
	}
	args := []string{"-c", "user.name=localswim", "-c", "user.email=localswim@local.invalid",
		"-c", "commit.gpgSign=false", "commit-tree", tree}
	if checkpoint.Pending != nil {
		// The private checkpoint's parent keeps the prepared artifact commit
		// reachable before the branch CAS, including during repository pruning.
		args = append(args, "-p", checkpoint.Pending.Commit)
	}
	object, err := publicationGit(repo.root, "", data, args...)
	next := strings.TrimSpace(string(object))
	if err != nil || !gitObjectID(next) {
		return "", gitPublicationError("publication_state_unavailable")
	}
	change := "create " + ref + " " + next + "\n"
	if oid != "" {
		change = "update " + ref + " " + next + " " + oid + "\n"
	}
	input := []byte(change)
	if _, err := publicationGit(repo.root, "", input, "update-ref", "--stdin"); err != nil {
		return "", gitPublicationError("git_ref_failed")
	}
	return next, nil
}

func (repo publicationRepository) pushTarget() (string, error) {
	// The pinned remote uses Git's URL-scoped configuration. An explicit
	// origin transport setting, including an empty proxy, cannot be copied
	// implicitly to that separate remote.
	const transport = `^remote\.origin\.(proxy|proxyauthmethod|receivepack|uploadpack|vcs|serveroption)$`
	_, err := publicationGitText(repo.root, "config", "--includes", "--name-only", "--get-regexp", transport)
	if err == nil {
		return "", gitPublicationError("unsupported_remote_transport")
	}
	if !gitConfigAbsent(err) {
		return "", gitPublicationError("git_configuration_unavailable")
	}
	url, err := publicationGitText(repo.root, "remote", "get-url", "--push", "--all", "origin")
	if err != nil || url == "" || strings.ContainsAny(url, "\r\n") {
		return "", gitPublicationError("remote_unavailable")
	}
	return url, nil
}

func publicationRemoteDigest(url string) string {
	digest := sha256.Sum256([]byte(url))
	return hex.EncodeToString(digest[:])
}

func (repo publicationRepository) finishPending(ref, oid string, checkpoint publicationCheckpoint) (publicationCheckpoint, error) {
	pending := checkpoint.Pending
	if err := publicationGitSafety(repo.root, nil); err != nil {
		return checkpoint, err
	}
	target, err := repo.pushTarget()
	if err != nil {
		return checkpoint, err
	}
	if publicationRemoteDigest(target) != pending.Remote {
		return checkpoint, gitPublicationError("remote_changed")
	}
	tip, err := publicationGitText(repo.root, "rev-parse", "--verify", "--end-of-options", pending.Branch)
	if err != nil || !gitObjectID(tip) {
		return checkpoint, gitPublicationError("publication_branch_changed")
	}
	if tip == pending.Parent && pending.Commit != pending.Parent {
		// The durable intent precedes this compare-and-swap. Recover an
		// interruption here only while the branch still names its exact parent.
		parent, err := publicationGitText(repo.root, "rev-parse", pending.Commit+"^")
		if err != nil || parent != pending.Parent {
			return checkpoint, gitPublicationError("publication_state_unavailable")
		}
		if _, err := publicationGit(repo.root, "", nil, "update-ref", pending.Branch, pending.Commit, pending.Parent); err != nil {
			return checkpoint, gitPublicationError("git_ref_failed")
		}
	} else if !repo.containsCommit(pending.Commit, tip) {
		return checkpoint, gitPublicationError("publication_branch_changed")
	}
	remote := pinnedPublicationRemote(target)
	if err := repo.push(remote, pending.Commit, pending.Branch); err != nil && !repo.remoteContains(remote, *pending) {
		return checkpoint, err
	}
	now := canonicalTime(time.Now())
	finished := publicationCheckpoint{Revision: checkpoint.Revision, LastSuccessAt: &now}
	if _, err := repo.saveCheckpoint(ref, oid, finished); err != nil {
		return checkpoint, err
	}
	return finished, nil
}

func (repo publicationRepository) containsCommit(commit, tip string) bool {
	if commit == tip {
		return true
	}
	_, err := publicationGit(repo.root, "", nil, "merge-base", "--is-ancestor", commit, tip)
	return err == nil
}

// A later publisher can advance this remote branch while this checkpoint is
// pending. Confirm the known descendant without rewriting any remote ref.
func (repo publicationRepository) remoteContains(remote publicationRemote, pending pendingPublication) bool {
	result, err := publicationGitConfigured(repo.root, "", nil, remote.configuration,
		"ls-remote", "--refs", "--exit-code", "--", remote.name, pending.Branch)
	oid, ref, found := strings.Cut(strings.TrimRight(string(result), "\r\n"), "\t")
	return err == nil && found && ref == pending.Branch && gitObjectID(oid) && repo.containsCommit(pending.Commit, oid)
}

func (repo publicationRepository) acceptPublication(stateRef, oid string, checkpoint publicationCheckpoint, capture publicationCapture, allowed func() bool) (publicationCheckpoint, error) {
	if capture.revision == 0 || capture.revision > maxSnapshotInteger || len(capture.files) == 0 {
		return checkpoint, gitPublicationError("capture_unavailable")
	}
	if capture.revision <= checkpoint.Revision {
		return checkpoint, nil
	}
	target, err := repo.pushTarget()
	if err != nil {
		return checkpoint, err
	}
	ref, previous, err := repo.head()
	if err != nil {
		return checkpoint, err
	}
	commit, err := repo.prepareCommit(previous, capture)
	if err != nil {
		return checkpoint, err
	}
	next := publicationCheckpoint{Revision: capture.revision, LastSuccessAt: checkpoint.LastSuccessAt}
	if !allowed() {
		return checkpoint, nil
	}
	// Persist the exact commit before any ref update or push. If the capture
	// already matches HEAD, the checkpoint still needs remote confirmation.
	next.Pending = &pendingPublication{Commit: commit, Parent: previous, Branch: ref, Remote: publicationRemoteDigest(target)}
	newOID, err := repo.saveCheckpoint(stateRef, oid, next)
	if err != nil {
		return checkpoint, err
	}
	return repo.finishPending(stateRef, newOID, next)
}
