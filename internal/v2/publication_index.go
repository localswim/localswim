package v2

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type publicationIndex struct {
	name string
	lock *os.File
	info os.FileInfo
}

// Use the worktree's own Git lock as well as the common publication lease.
// Ordinary Git writers know about index.lock, including in linked worktrees.
func (repo publicationRepository) lockIndex() (*publicationIndex, error) {
	name, err := repo.gitText("rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil || !filepath.IsAbs(name) {
		return nil, gitPublicationError("git_index_failed")
	}
	lock, err := os.OpenFile(name+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if errors.Is(err, os.ErrExist) {
		return nil, gitPublicationError("publication_index_busy")
	}
	if err != nil {
		return nil, gitPublicationError("git_index_failed")
	}
	info, err := lock.Stat()
	if err != nil {
		_ = lock.Close()
		_ = os.Remove(name + ".lock")
		return nil, gitPublicationError("git_index_failed")
	}
	return &publicationIndex{name, lock, info}, nil
}

func (index *publicationIndex) close() {
	if index.lock != nil {
		_ = index.lock.Close()
	}
	if index.info == nil {
		return
	}
	name := index.name + ".lock"
	if current, err := os.Lstat(name); err == nil && os.SameFile(index.info, current) {
		_ = os.Remove(name)
	}
}

// Scan names without retaining unrelated staged paths or placing an inventory
// on the command line. Git emits raw path bytes separated by NUL.
type gitPathOutput struct {
	partial []byte
	visit   func(string)
}

func (out *gitPathOutput) Write(data []byte) (int, error) {
	length := len(data)
	for len(data) != 0 {
		name, rest, found := bytes.Cut(data, []byte{0})
		if len(out.partial)+len(name) > 256*1024 {
			return 0, errors.New("Git path exceeds limit")
		}
		out.partial = append(out.partial, name...)
		if !found {
			break
		}
		if len(out.partial) == 0 {
			return 0, errors.New("Git returned an empty path")
		}
		out.visit(string(out.partial))
		out.partial = out.partial[:0]
		data = rest
	}
	return length, nil
}

func (repo publicationRepository) indexMatches(index, tree string, names []string) (bool, error) {
	config, err := repo.gitText("config", "--type=bool", "--get", "core.ignoreCase")
	if err != nil && !gitConfigAbsent(err) {
		return false, gitPublicationError("git_index_failed")
	}
	normalize := func(name string) string { return name }
	if config == "true" {
		normalize = strings.ToLower
	}
	exact := make(map[string]bool, len(names))
	ancestors := make(map[string]bool)
	for _, name := range names {
		name = normalize(name)
		exact[name] = true
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			ancestors[parent] = true
		}
	}
	matches := true
	output := gitPathOutput{visit: func(name string) {
		name = normalize(name)
		if exact[name] || ancestors[name] {
			matches = false
		}
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if exact[parent] {
				matches = false
				break
			}
		}
	}}
	var stderr gitStderrPresence
	err = repo.gitOutput(index, nil, nil, &output, &stderr,
		"diff-index", "--cached", "--name-only", "--no-renames", "--no-ext-diff", "--no-textconv",
		"--ignore-submodules=none", "--ita-visible-in-index", "-z", tree, "--")
	if err != nil || stderr.seen || len(output.partial) != 0 {
		return false, gitPublicationError("git_index_failed")
	}
	return matches, nil
}

func (repo publicationRepository) checkPublicationIndex(ref, previous string, names []string) error {
	branch, tip, err := repo.head()
	if err != nil || branch != ref || tip != previous {
		return gitPublicationError("publication_branch_changed")
	}
	matches, err := repo.indexMatches("", previous, names)
	if err != nil {
		return err
	}
	if !matches {
		return gitPublicationError("publication_index_overlap")
	}
	return nil
}

func (repo publicationRepository) preparePublication(ref, previous string, capture publicationCapture) (string, []string, error) {
	names, err := publicationInventory(repo.prefix, capture.files)
	if err != nil {
		return "", nil, err
	}
	if err := repo.gitSafety(names); err != nil {
		return "", nil, err
	}
	index, err := repo.lockIndex()
	if err != nil {
		return "", nil, err
	}
	defer index.close()
	if err := repo.checkPublicationIndex(ref, previous, names); err != nil {
		return "", nil, err
	}
	commit, err := repo.prepareCommit(previous, capture, names)
	return commit, names, err
}

func copyPublicationIndex(source, destination string) error {
	info, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return gitPublicationError("git_index_failed")
	}
	input, err := os.Open(source)
	if err != nil {
		return gitPublicationError("git_index_failed")
	}
	defer input.Close()
	current, err := input.Stat()
	if err != nil || !os.SameFile(info, current) {
		return gitPublicationError("git_index_failed")
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return gitPublicationError("git_index_failed")
	}
	_, err = io.Copy(output, input)
	if errors.Join(err, output.Close()) != nil {
		return gitPublicationError("git_index_failed")
	}
	return nil
}

func (repo publicationRepository) prepareLiveIndex(index *publicationIndex, commit string, names []string) error {
	directory, err := os.MkdirTemp(filepath.Dir(index.name), "localswim-index-*")
	if err != nil {
		return gitPublicationError("git_index_failed")
	}
	defer os.RemoveAll(directory)
	temporary := filepath.Join(directory, "index")
	if err := copyPublicationIndex(index.name, temporary); err != nil {
		return err
	}
	paths := []byte(strings.Join(names, "\x00") + "\x00")
	if _, err := repo.git(temporary, paths, "--literal-pathspecs", "reset", "--quiet",
		"--no-refresh", commit, "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
		return gitPublicationError("git_index_failed")
	}
	if matches, err := repo.indexMatches(temporary, commit, names); err != nil || !matches {
		return gitPublicationError("git_index_failed")
	}
	file, err := os.Open(temporary)
	if err != nil {
		return gitPublicationError("git_index_failed")
	}
	_, err = io.Copy(index.lock, file)
	if errors.Join(err, file.Close(), index.lock.Sync()) != nil {
		return gitPublicationError("git_index_failed")
	}
	err = index.lock.Close()
	index.lock = nil
	if err != nil {
		return gitPublicationError("git_index_failed")
	}
	return nil
}

func (repo publicationRepository) advancePublication(ref, previous, commit string, names []string) error {
	index, err := repo.lockIndex()
	if err != nil {
		return err
	}
	defer index.close()
	if err := repo.checkPublicationIndex(ref, previous, names); err != nil || commit == previous {
		return err
	}
	if err := repo.prepareLiveIndex(index, commit, names); err != nil {
		return err
	}
	if branch, tip, err := repo.head(); err != nil || branch != ref || tip != previous {
		return gitPublicationError("publication_branch_changed")
	}
	_, updateErr := repo.git("", nil, "update-ref", ref, commit, previous)
	branch, tip, err := repo.head()
	if updateErr != nil && err == nil && branch == ref && tip == previous {
		return gitPublicationError("git_ref_failed")
	}
	// Ref and index use distinct Git transactions. Confirm the branch before
	// installing the prepared index. If either outcome cannot be proved, stop
	// before push and leave any existing index entries for operator inspection.
	if err != nil || branch != ref || tip != commit || replaceAtomic(index.name+".lock", index.name) != nil {
		return gitPublicationError("publication_index_uncertain")
	}
	index.info = nil
	return nil
}

func (repo publicationRepository) confirmPublicationIndex(pending pendingPublication, tip string) error {
	index, err := repo.lockIndex()
	if err != nil {
		return err
	}
	defer index.close()
	branch, err := repo.gitText("symbolic-ref", "--quiet", "HEAD")
	if err != nil && !gitConfigAbsent(err) {
		return gitPublicationError("publication_index_uncertain")
	}
	if branch != pending.SourceRef {
		// Another checkout cannot establish index consistency for the
		// pending branch after its ref advanced.
		if pending.Commit != pending.Parent {
			return gitPublicationError("publication_index_uncertain")
		}
		return nil
	}
	err = repo.checkPublicationIndex(pending.SourceRef, tip, pending.Names)
	if err == gitPublicationError("publication_index_overlap") && pending.Commit != pending.Parent {
		return gitPublicationError("publication_index_uncertain")
	}
	return err
}
