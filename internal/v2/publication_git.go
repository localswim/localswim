package v2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const publicationGitTimeout = 30 * time.Second
const publicationLockWait = 5 * time.Second

type publicationGitError string

func (e publicationGitError) Error() string { return string(e) }

// All diagnostics are fixed codes. Git output can contain credentials, paths,
// remote errors, or captured content and must never cross the HTTP boundary.
func gitPublicationError(code string) error { return publicationGitError(code) }

type boundedGitOutput struct {
	data bytes.Buffer
}

func (output *boundedGitOutput) Write(data []byte) (int, error) {
	if output.data.Len()+len(data) > 64*1024 {
		return 0, errors.New("Git output exceeds limit")
	}
	return output.data.Write(data)
}

func publicationGit(cwd, index string, input []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), publicationGitTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = cwd
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	if index != "" {
		command.Env = append(command.Env, "GIT_INDEX_FILE="+index)
	}
	command.Stdin = bytes.NewReader(input)
	var output boundedGitOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	err := command.Run()
	return output.data.Bytes(), err
}

func publicationGitText(cwd string, args ...string) (string, error) {
	data, err := publicationGit(cwd, "", nil, args...)
	return strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r"), err
}

// Shared by board publication and future workspace-artifact publication. The
// common directory and persistent lock name match the V1 publisher, including
// linked worktrees. Never initialize a lock byte before acquiring the OS lock.
func acquirePublicationRepository(cwd string, wait time.Duration) (*os.File, error) {
	common, err := publicationGitText(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || common == "" {
		return nil, gitPublicationError("repository_unavailable")
	}
	path := filepath.Join(common, "localswim-autopush.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, gitPublicationError("repository_lock_unavailable")
	}
	info, err := file.Stat()
	named, namedErr := os.Lstat(path)
	if err != nil || namedErr != nil || !named.Mode().IsRegular() || !os.SameFile(info, named) {
		_ = file.Close()
		return nil, gitPublicationError("repository_lock_unavailable")
	}
	deadline := time.Now().Add(wait)
	for {
		err = lockWriter(file)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, errStoreBusy) || !time.Now().Before(deadline) {
			_ = file.Close()
			return nil, gitPublicationError("repository_busy")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func releasePublicationRepository(file *os.File) {
	_ = unlockWriter(file)
	_ = file.Close()
}

func publicationGitSafety(cwd string, paths []string) error {
	hooks, err := publicationGitText(cwd, "config", "--get", "core.hooksPath")
	if err == nil && hooks != "" {
		return gitPublicationError("unsupported_hooks")
	}
	if err != nil && !gitConfigAbsent(err) {
		return gitPublicationError("git_configuration_unavailable")
	}
	monitor, err := publicationGitText(cwd, "config", "--type=bool", "--get", "core.fsmonitor")
	if err == nil && monitor != "false" || err != nil && !gitConfigAbsent(err) {
		return gitPublicationError("unsupported_fsmonitor")
	}
	directory, err := publicationGitText(cwd, "rev-parse", "--path-format=absolute", "--git-path", "hooks")
	if err != nil {
		return gitPublicationError("git_configuration_unavailable")
	}
	entries, err := os.ReadDir(directory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return gitPublicationError("git_configuration_unavailable")
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sample") && !entry.IsDir() {
			return gitPublicationError("unsupported_hooks")
		}
	}
	// Bound each result independently so a valid large audit inventory does not
	// overflow the diagnostic cap. All batches finish before index construction.
	for start := 0; start < len(paths); {
		end, size := start, 0
		for end < len(paths) && (end == start || size+len(paths[end])+1 <= 16*1024) {
			size += len(paths[end]) + 1
			end++
		}
		batch := paths[start:end]
		input := []byte(strings.Join(batch, "\x00") + "\x00")
		data, err := publicationGit(cwd, "", input, "check-attr", "-z", "--stdin", "filter")
		fields := strings.Split(string(data), "\x00")
		if err != nil || len(fields) != len(batch)*3+1 || fields[len(fields)-1] != "" {
			return gitPublicationError("git_attributes_unavailable")
		}
		for index, path := range batch {
			value := fields[index*3+2]
			if fields[index*3] != path || fields[index*3+1] != "filter" {
				return gitPublicationError("git_attributes_unavailable")
			}
			if value != "unspecified" && value != "unset" {
				return gitPublicationError("unsupported_filter")
			}
		}
		start = end
	}
	return nil
}

func gitConfigAbsent(err error) bool {
	var status *exec.ExitError
	return errors.As(err, &status) && status.ExitCode() == 1
}

func publicationPaths(dataRoot, repository string, archive *verifiedBackupArchive) ([]string, error) {
	prefix, err := filepath.Rel(repository, dataRoot)
	if err != nil || !filepath.IsLocal(prefix) {
		return nil, gitPublicationError("repository_unavailable")
	}
	paths := []string{filepath.ToSlash(filepath.Join(prefix, "boards", archive.state.Board.ID+".json"))}
	for _, item := range archive.state.Attachments {
		paths = append(paths, filepath.ToSlash(filepath.Join(prefix, attachmentPath(item))))
	}
	slices.Sort(paths)
	return paths, nil
}

// A private index overlays only the exact capture inventory onto HEAD. Hashing
// stdin without --path stores the verified bytes verbatim, independent of EOL
// conversion and attributes. The caller's index and live files are never opened
// for staging or rewritten, including on commit and push failure.
func publishVerifiedBackup(dataRoot string, item backup) error {
	archive, err := openVerifiedBackupArchive(dataRoot, item)
	if err != nil {
		return gitPublicationError("backup_integrity")
	}
	defer archive.close()
	repository, err := publicationGitText(dataRoot, "rev-parse", "--show-toplevel")
	if err != nil || repository == "" {
		return gitPublicationError("repository_unavailable")
	}
	lease, err := acquirePublicationRepository(repository, publicationLockWait)
	if err != nil {
		return err
	}
	defer releasePublicationRepository(lease)
	paths, err := publicationPaths(dataRoot, repository, archive)
	if err != nil {
		return err
	}
	if err := publicationGitSafety(repository, paths); err != nil {
		return err
	}
	ref, err := publicationGitText(repository, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || !strings.HasPrefix(ref, "refs/heads/") {
		return gitPublicationError("branch_unavailable")
	}
	if _, err := publicationGitText(repository, "remote", "get-url", "origin"); err != nil {
		return gitPublicationError("remote_unavailable")
	}
	previous, err := publicationGitText(repository, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return gitPublicationError("head_unavailable")
	}
	directory, err := os.MkdirTemp("", "localswim-publication-*")
	if err != nil {
		return gitPublicationError("capture_unavailable")
	}
	defer os.RemoveAll(directory)
	index := filepath.Join(directory, "index")
	if _, err := publicationGit(repository, index, nil, "read-tree", previous); err != nil {
		return gitPublicationError("git_index_failed")
	}
	prefix, _ := filepath.Rel(repository, dataRoot)
	contents := func(path string, data []byte) error {
		hash, err := publicationGit(repository, index, data, "hash-object", "-w", "--stdin")
		if err != nil {
			return gitPublicationError("git_object_failed")
		}
		oid := strings.TrimSpace(string(hash))
		name := filepath.ToSlash(filepath.Join(prefix, path))
		// --cacheinfo without --replace refuses file/directory conflicts with
		// inherited HEAD entries. Confirm the exact path, mode, and object before
		// the private index can become a tree, even if Git reports success.
		if _, err := publicationGit(repository, index, nil, "update-index", "--add", "--cacheinfo", "100644,"+oid+","+name); err != nil {
			return gitPublicationError("git_index_failed")
		}
		staged, err := publicationGit(repository, index, nil, "--literal-pathspecs", "ls-files", "--stage", "-z", "--", name)
		if err != nil || string(staged) != "100644 "+oid+" 0\t"+name+"\x00" {
			return gitPublicationError("git_index_failed")
		}
		return nil
	}
	if err := contents("boards/"+archive.state.Board.ID+".json", archive.snapshotData); err != nil {
		return err
	}
	for _, attachment := range archive.state.Attachments {
		data, err := archive.attachmentData(attachment)
		if err != nil {
			return gitPublicationError("backup_integrity")
		}
		if err := contents(attachmentPath(attachment), data); err != nil {
			return err
		}
	}
	tree, err := publicationGit(repository, index, nil, "write-tree")
	if err != nil {
		return gitPublicationError("git_tree_failed")
	}
	oldTree, err := publicationGitText(repository, "rev-parse", previous+"^{tree}")
	if err != nil {
		return gitPublicationError("git_tree_failed")
	}
	commit := previous
	if strings.TrimSpace(string(tree)) != oldTree {
		data, err := publicationGit(repository, index, []byte("localswim backup "+item.ID+"\n"), "commit-tree", strings.TrimSpace(string(tree)), "-p", previous)
		if err != nil {
			return gitPublicationError("git_commit_failed")
		}
		commit = strings.TrimSpace(string(data))
		if _, err := publicationGit(repository, index, nil, "update-ref", ref, commit, previous); err != nil {
			return gitPublicationError("git_ref_failed")
		}
	}
	// Push the exact accepted commit. Never retry a failed or timed-out push.
	if _, err := publicationGit(repository, index, nil, "push", "--no-follow-tags", "--recurse-submodules=no", "--", "origin", commit+":"+ref); err != nil {
		return gitPublicationError("git_push_failed")
	}
	return nil
}
