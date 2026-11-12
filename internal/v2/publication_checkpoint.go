package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path/filepath"
	"strings"
	"time"
)

type pendingPublication struct {
	Commit    string            `json:"commit"`
	Parent    string            `json:"parent"`
	SourceRef string            `json:"source_ref"`
	Target    publicationTarget `json:"target"`
	Names     []string          `json:"-"`
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
	if pending := checkpoint.Pending; pending != nil {
		if !gitObjectID(pending.Commit) || !gitObjectID(pending.Parent) ||
			len(pending.Target.URLDigest) != 64 || !validPublicationDigest(pending.Target.URLDigest) ||
			!strings.Contains(headers, "\nparent "+pending.Commit+"\n") {
			return publicationCheckpoint{}, "", gitPublicationError("publication_state_unavailable")
		}
		_, sourceErr := repo.publicationBranchRef(pending.SourceRef)
		_, targetErr := repo.publicationBranchRef(pending.Target.Ref)
		if sourceErr != nil || targetErr != nil || !repo.validRemoteName(pending.Target.Remote) {
			return publicationCheckpoint{}, "", gitPublicationError("publication_state_unavailable")
		}
		names, err := repo.checkpointInventory(oid)
		if err != nil {
			return publicationCheckpoint{}, "", err
		}
		pending.Names = names
	}
	return checkpoint, oid, nil
}

func (repo publicationRepository) checkpointInventory(oid string) ([]string, error) {
	output := boundedGitOutput{limit: maxSnapshotBytes}
	err := runPublicationGitOutput(repo.root, "", nil, nil, &output, io.Discard, "cat-file", "blob", oid+":inventory")
	data, terminated := strings.CutSuffix(output.data.String(), "\x00")
	if err != nil || !terminated {
		return nil, gitPublicationError("publication_state_unavailable")
	}
	names := strings.Split(data, "\x00")
	if !validPublicationNames(names) {
		return nil, gitPublicationError("publication_state_unavailable")
	}
	prefix := repo.checkpointInventoryPrefix()
	for i := range names {
		names[i] = prefix + names[i]
	}
	return names, nil
}

func (repo publicationRepository) checkpointInventoryPrefix() string {
	if repo.prefix == "." || repo.prefix == "" {
		return ""
	}
	return filepath.ToSlash(repo.prefix) + "/"
}

func (repo publicationRepository) checkpointTree(pending *pendingPublication) (string, error) {
	var entry []byte
	if pending != nil {
		// The checkpoint tree retains the exact scope independently of the
		// changed tree paths. A no-op path can still have operator staging.
		// Use data-root-relative names so a deep worktree prefix does not
		// multiply the checkpoint size with every audited attachment.
		prefix := repo.checkpointInventoryPrefix()
		relative := make([]string, len(pending.Names))
		for i, name := range pending.Names {
			value, found := strings.CutPrefix(name, prefix)
			if !found {
				return "", gitPublicationError("publication_state_unavailable")
			}
			relative[i] = value
		}
		names := []byte(strings.Join(relative, "\x00") + "\x00")
		if len(relative) == 0 || len(names) > maxSnapshotBytes {
			return "", gitPublicationError("publication_state_unavailable")
		}
		hash, err := publicationGit(repo.root, "", names, "hash-object", "-w", "--stdin")
		blob := strings.TrimSpace(string(hash))
		if err != nil || !gitObjectID(blob) {
			return "", gitPublicationError("publication_state_unavailable")
		}
		entry = []byte("100644 blob " + blob + "\tinventory\x00")
	}
	output, err := publicationGit(repo.root, "", entry, "mktree", "-z")
	tree := strings.TrimSpace(string(output))
	if err != nil || !gitObjectID(tree) {
		return "", gitPublicationError("publication_state_unavailable")
	}
	return tree, nil
}

func (repo publicationRepository) saveCheckpoint(ref, oid string, checkpoint publicationCheckpoint) (string, error) {
	data, err := marshalCanonical(checkpoint)
	if err != nil {
		return "", gitPublicationError("publication_state_unavailable")
	}
	tree, err := repo.checkpointTree(checkpoint.Pending)
	if err != nil {
		return "", err
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

func publicationRemoteDigest(url string) string {
	digest := sha256.Sum256([]byte(url))
	return hex.EncodeToString(digest[:])
}

func (repo publicationRepository) pushPending(pending pendingPublication, selection publicationSelection) error {
	if err := publicationGitSafety(repo.root, nil); err != nil {
		return err
	}
	destination, err := repo.resolveDestination(pending.SourceRef, selection)
	if err != nil {
		return err
	}
	if destination.target != pending.Target {
		return gitPublicationError("publication_destination_changed")
	}
	tip, err := publicationGitText(repo.root, "rev-parse", "--verify", "--end-of-options", pending.SourceRef)
	if err != nil || !gitObjectID(tip) {
		return gitPublicationError("publication_branch_changed")
	}
	if tip == pending.Parent && pending.Commit != pending.Parent {
		// The durable intent precedes this compare-and-swap. Recover an
		// interruption here only while the branch still names its exact parent.
		parent, err := publicationGitText(repo.root, "rev-parse", pending.Commit+"^")
		if err != nil || parent != pending.Parent {
			return gitPublicationError("publication_state_unavailable")
		}
		if err := repo.advancePublication(pending.SourceRef, pending.Parent, pending.Commit, pending.Names); err != nil {
			return err
		}
	} else if !repo.containsCommit(pending.Commit, tip) {
		return gitPublicationError("publication_branch_changed")
	} else if err := repo.confirmPublicationIndex(pending, tip); err != nil {
		return err
	}
	remote := pinnedPublicationRemote(destination.url)
	if err := repo.push(remote, pending.Commit, pending.Target.Ref); err != nil && !repo.remoteContains(remote, pending) {
		return err
	}
	return nil
}

func (repo publicationRepository) finishPending(ref, oid string, checkpoint publicationCheckpoint, selection publicationSelection) (publicationCheckpoint, error) {
	if err := repo.pushPending(*checkpoint.Pending, selection); err != nil {
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
		"ls-remote", "--refs", "--exit-code", "--", remote.name, pending.Target.Ref)
	oid, ref, found := strings.Cut(strings.TrimRight(string(result), "\r\n"), "\t")
	return err == nil && found && ref == pending.Target.Ref && gitObjectID(oid) && repo.containsCommit(pending.Commit, oid)
}

func (repo publicationRepository) preparePending(stateRef, oid string, checkpoint publicationCheckpoint, capture publicationCapture, selection publicationSelection, allowed func() bool) (publicationCheckpoint, string, error) {
	if capture.revision == 0 || capture.revision > maxSnapshotInteger || len(capture.files) == 0 {
		return checkpoint, oid, gitPublicationError("capture_unavailable")
	}
	if capture.revision <= checkpoint.Revision {
		return checkpoint, oid, nil
	}
	ref, previous, err := repo.head()
	if err != nil {
		return checkpoint, oid, err
	}
	destination, err := repo.resolveDestination(ref, selection)
	if err != nil {
		return checkpoint, oid, err
	}
	commit, names, err := repo.preparePublication(ref, previous, capture)
	if err != nil {
		return checkpoint, oid, err
	}
	next := publicationCheckpoint{Revision: capture.revision, LastSuccessAt: checkpoint.LastSuccessAt}
	if !allowed() {
		return checkpoint, oid, nil
	}
	// Persist the exact commit before any ref update or push. If the capture
	// already matches HEAD, the checkpoint still needs remote confirmation.
	next.Pending = &pendingPublication{
		Commit: commit, Parent: previous, SourceRef: ref, Target: destination.target, Names: names,
	}
	newOID, err := repo.saveCheckpoint(stateRef, oid, next)
	if err != nil {
		return checkpoint, oid, err
	}
	return next, newOID, nil
}

func (repo publicationRepository) acceptPublication(ref, oid string, checkpoint publicationCheckpoint, capture publicationCapture, selection publicationSelection, allowed func() bool) (publicationCheckpoint, error) {
	next, newOID, err := repo.preparePending(ref, oid, checkpoint, capture, selection, allowed)
	if err != nil || next.Pending == nil {
		return next, err
	}
	return repo.finishPending(ref, newOID, next, selection)
}
