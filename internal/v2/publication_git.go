package v2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
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
	data  bytes.Buffer
	limit int
}

func (output *boundedGitOutput) Write(data []byte) (int, error) {
	if output.data.Len()+len(data) > output.limit {
		return 0, errors.New("Git output exceeds limit")
	}
	return output.data.Write(data)
}

func publicationGit(cwd, index string, input []byte, args ...string) ([]byte, error) {
	return publicationGitConfigured(cwd, index, input, nil, args...)
}

func publicationGitConfigured(cwd, index string, input []byte, configuration []string, args ...string) ([]byte, error) {
	return runPublicationGit(cwd, index, input, configuration, io.Discard, args...)
}

func runPublicationGit(cwd, index string, input []byte, configuration []string, stderr io.Writer, args ...string) ([]byte, error) {
	output := boundedGitOutput{limit: 64 * 1024}
	err := runPublicationGitOutput(cwd, index, input, configuration, &output, stderr, args...)
	return output.data.Bytes(), err
}

func runPublicationGitOutput(cwd, index string, input []byte, configuration []string, stdout, stderr io.Writer, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), publicationGitTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	configurePublicationGitCommand(command)
	command.Dir = cwd
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1")
	command.Env = append(command.Env, configuration...)
	if index != "" {
		command.Env = append(command.Env, "GIT_INDEX_FILE="+index)
	}
	command.Stdin = bytes.NewReader(input)
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

func publicationGitText(cwd string, args ...string) (string, error) {
	data, err := publicationGit(cwd, "", nil, args...)
	return strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r"), err
}

// Shared by manual, board and workspace publication. The
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
		if err := publicationGitIgnore(cwd, batch); err != nil {
			return err
		}
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

// Git can warn that it skipped an ignore source and still return no matches.
// Keep only stderr presence so warnings cannot expose paths or other details.
type gitStderrPresence struct {
	seen bool
}

func (stderr *gitStderrPresence) Write(data []byte) (int, error) {
	stderr.seen = stderr.seen || len(data) > 0
	return len(data), nil
}

func publicationGitIgnore(cwd string, paths []string) error {
	// Apply the worktree's ignore rules even to tracked or staged paths.
	// Staging uses a private index, so the caller's index cannot grant admission.
	// A leading ./ makes each path literal to check-ignore, which rejects the
	// global --literal-pathspecs option.
	input := []byte("./" + strings.Join(paths, "\x00./") + "\x00")
	var stderr gitStderrPresence
	data, err := runPublicationGit(cwd, "", input, nil, &stderr, "check-ignore", "--no-index", "-z", "--stdin")
	if stderr.seen {
		return gitPublicationError("git_ignore_unavailable")
	}
	if err == nil && len(data) != 0 {
		return gitPublicationError("publication_path_ignored")
	}
	var status *exec.ExitError
	if errors.As(err, &status) && status.ExitCode() == 1 && len(data) == 0 {
		return nil
	}
	return gitPublicationError("git_ignore_unavailable")
}

func gitConfigAbsent(err error) bool {
	var status *exec.ExitError
	return errors.As(err, &status) && status.ExitCode() == 1
}

type publicationFile struct {
	path string
	read func() ([]byte, error)
}

type publicationCapture struct {
	revision uint64
	message  string
	files    []publicationFile
	close    func()
}

type publicationRepository struct {
	root   string
	prefix string
	lease  *os.File
}

func lockPublication(dataRoot string) (publicationRepository, error) {
	root, err := publicationGitText(dataRoot, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return publicationRepository{}, gitPublicationError("repository_unavailable")
	}
	prefix, err := filepath.Rel(root, dataRoot)
	if err != nil || !filepath.IsLocal(prefix) {
		return publicationRepository{}, gitPublicationError("repository_unavailable")
	}
	lease, err := acquirePublicationRepository(root, publicationLockWait)
	if err != nil {
		return publicationRepository{}, err
	}
	return publicationRepository{root: root, prefix: prefix, lease: lease}, nil
}

func (repo publicationRepository) head() (string, string, error) {
	ref, err := publicationGitText(repo.root, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || !strings.HasPrefix(ref, "refs/heads/") {
		return "", "", gitPublicationError("branch_unavailable")
	}
	previous, err := publicationGitText(repo.root, "rev-parse", "--verify", ref)
	if err != nil {
		return "", "", gitPublicationError("head_unavailable")
	}
	return ref, previous, nil
}

type publicationRemote struct {
	name          string
	configuration []string
}

func pinnedPublicationRemote(target string) publicationRemote {
	name := "localswim-publication-" + newID()
	marker := name + "::"
	// Git applies insteadOf once. The full unique marker wins over shorter
	// rules, so the resolved URL is never reinterpreted as a rewrite input.
	// Explicit pushurl skips pushInsteadOf. Keep the URL in child config.
	return publicationRemote{name: name, configuration: []string{
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=remote." + name + ".url",
		"GIT_CONFIG_VALUE_0=" + marker,
		"GIT_CONFIG_KEY_1=remote." + name + ".pushurl",
		"GIT_CONFIG_VALUE_1=" + marker,
		"GIT_CONFIG_KEY_2=url." + target + ".insteadOf",
		"GIT_CONFIG_VALUE_2=" + marker,
	}}
}

func (repo publicationRepository) push(remote publicationRemote, commit, ref string) error {
	if _, err := publicationGitConfigured(repo.root, "", nil, remote.configuration, "-c", "remote."+remote.name+".mirror=false",
		"push", "--no-follow-tags", "--recurse-submodules=no", "--", remote.name, commit+":"+ref); err != nil {
		return gitPublicationError("git_push_failed")
	}
	return nil
}

func publicationInventory(prefix string, files []publicationFile) ([]string, error) {
	if len(files) == 0 {
		return nil, gitPublicationError("capture_unavailable")
	}
	names := make([]string, len(files))
	for index, file := range files {
		relative := filepath.ToSlash(file.path)
		if file.read == nil || !filepath.IsLocal(file.path) || !fs.ValidPath(relative) || relative == "." {
			return nil, gitPublicationError("capture_unavailable")
		}
		names[index] = filepath.ToSlash(filepath.Join(prefix, file.path))
	}
	if !validPublicationNames(names) {
		return nil, gitPublicationError("capture_unavailable")
	}
	return names, nil
}

func validPublicationNames(names []string) bool {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if !fs.ValidPath(name) || name == "." || strings.ContainsRune(name, 0) || seen[name] {
			return false
		}
		seen[name] = true
	}
	for _, name := range names {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return false
			}
		}
	}
	return len(names) != 0
}

// A private index overlays only the exact capture inventory onto the accepted
// parent. Hashing stdin without --path stores captured bytes verbatim.
func (repo publicationRepository) prepareCommit(previous string, capture publicationCapture, names []string) (string, error) {
	directory, err := os.MkdirTemp("", "localswim-publication-*")
	if err != nil {
		return "", gitPublicationError("capture_unavailable")
	}
	defer os.RemoveAll(directory)
	index := filepath.Join(directory, "index")
	if _, err := publicationGit(repo.root, index, nil, "read-tree", previous); err != nil {
		return "", gitPublicationError("git_index_failed")
	}
	for position, file := range capture.files {
		data, err := file.read()
		if err != nil {
			return "", gitPublicationError("backup_integrity")
		}
		hash, err := publicationGit(repo.root, index, data, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", gitPublicationError("git_object_failed")
		}
		oid := strings.TrimSpace(string(hash))
		name := names[position]
		// --cacheinfo without --replace refuses file/directory conflicts with
		// inherited entries. Verify exact mode, path, and object before writing.
		if _, err := publicationGit(repo.root, index, nil, "update-index", "--add", "--cacheinfo", "100644,"+oid+","+name); err != nil {
			return "", gitPublicationError("git_index_failed")
		}
		staged, err := publicationGit(repo.root, index, nil, "--literal-pathspecs", "ls-files", "--stage", "-z", "--", name)
		if err != nil || string(staged) != "100644 "+oid+" 0\t"+name+"\x00" {
			return "", gitPublicationError("git_index_failed")
		}
	}
	tree, err := publicationGit(repo.root, index, nil, "write-tree")
	if err != nil {
		return "", gitPublicationError("git_tree_failed")
	}
	oldTree, err := publicationGitText(repo.root, "rev-parse", previous+"^{tree}")
	if err != nil {
		return "", gitPublicationError("git_tree_failed")
	}
	if strings.TrimSpace(string(tree)) == oldTree {
		return previous, nil
	}
	data, err := publicationGit(repo.root, index, []byte(capture.message+"\n"), "commit-tree", strings.TrimSpace(string(tree)), "-p", previous)
	if err != nil {
		return "", gitPublicationError("git_commit_failed")
	}
	return strings.TrimSpace(string(data)), nil
}

func publishVerifiedBackup(dataRoot string, item backup) error {
	archive, err := openVerifiedBackupArchive(dataRoot, item)
	if err != nil {
		return gitPublicationError("backup_integrity")
	}
	defer archive.close()
	repo, err := lockPublication(dataRoot)
	if err != nil {
		return err
	}
	defer releasePublicationRepository(repo.lease)
	files := []publicationFile{{path: "boards/" + archive.state.Board.ID + ".json", read: func() ([]byte, error) {
		return archive.snapshotData, nil
	}}}
	for _, attachment := range archive.state.Attachments {
		files = append(files, publicationFile{path: attachmentPath(attachment), read: func() ([]byte, error) {
			return archive.attachmentData(attachment)
		}})
	}
	if _, err := publicationGitText(repo.root, "remote", "get-url", "origin"); err != nil {
		return gitPublicationError("remote_unavailable")
	}
	ref, previous, err := repo.head()
	if err != nil {
		return err
	}
	commit, err := repo.publishCapture(ref, previous, publicationCapture{message: "localswim backup " + item.ID, files: files})
	if err != nil {
		return err
	}
	return repo.push(publicationRemote{name: "origin"}, commit, ref)
}
