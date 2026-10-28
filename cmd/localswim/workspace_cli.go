package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/archive"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	W "github.com/localswim/localswim/internal/transport/cli/workspace"
)

const workspaceCLIHelp = `  localswim --workspace DIR [--json] project list
  localswim --workspace DIR [--json] project create --key KEY --label LABEL --root DIR
  localswim --workspace DIR [--project PROJECT] [--json] board list
  localswim --workspace DIR --project PROJECT [--json] board create --key KEY --label LABEL --root DIR
  localswim --workspace DIR --project PROJECT [--json] board register --key KEY --label LABEL --root DIR [--expected-identity FILE]
  localswim --workspace DIR --project PROJECT --board BOARD [--json] board status [--root DIR] [--expected-identity FILE]
  localswim --workspace DIR --project PROJECT --board BOARD [--json] board read
  localswim --workspace DIR --project PROJECT --board BOARD [--json] card|comment|attachment|export|operation COMMAND ...
Workspace selection and --data are mutually exclusive. Domain commands require both keys.
An expected identity file contains only installationId, boardId and incarnation.`

type workspaceCLIOwner interface{ Close() error }

var workspaceCLICleanup struct {
	sync.Mutex
	clients  []workspaceCLIOwner
	retrying bool
}

func finishWorkspaceCLI(client workspaceCLIOwner) {
	if client == nil || client.Close() == nil {
		return
	}
	workspaceCLICleanup.Lock()
	retainWorkspaceCLI(client)
	workspaceCLICleanup.Unlock()
}

// All private owners are stable pointers, including Client and Location.
func retainWorkspaceCLI(client workspaceCLIOwner) {
	for _, owner := range workspaceCLICleanup.clients {
		if owner == client {
			return
		}
	}
	workspaceCLICleanup.clients = append(workspaceCLICleanup.clients, client)
}

func retryWorkspaceCLICleanup() bool {
	workspaceCLICleanup.Lock()
	if workspaceCLICleanup.retrying {
		workspaceCLICleanup.Unlock()
		return false
	}
	workspaceCLICleanup.retrying = true
	owners := workspaceCLICleanup.clients
	workspaceCLICleanup.clients = nil
	workspaceCLICleanup.Unlock()
	var pending []workspaceCLIOwner
	for _, client := range owners {
		if client.Close() != nil {
			pending = append(pending, client)
		}
	}
	workspaceCLICleanup.Lock()
	for _, client := range pending {
		retainWorkspaceCLI(client)
	}
	workspaceCLICleanup.retrying = false
	ready := len(workspaceCLICleanup.clients) == 0
	workspaceCLICleanup.Unlock()
	return ready
}

// Selection flags belong before the command. No command can override authority.
func parseWorkspaceLocal(args []string, q *localInvocation) error {
	seen := map[string]bool{}
	i := 0
	jsonMode := false
	for i < len(args) && strings.HasPrefix(args[i], "--") {
		flag := args[i]
		if seen[flag] {
			return errComposition
		}
		seen[flag] = true
		i++
		if flag == "--json" {
			jsonMode = true
			continue
		}
		if i >= len(args) || args[i] == "" || strings.ContainsAny(args[i], "\x00\r\n") {
			return errComposition
		}
		switch flag {
		case "--workspace":
			q.workspace = args[i]
		case "--project":
			q.project = args[i]
		case "--board":
			q.board = args[i]
		default:
			return errComposition
		}
		i++
	}
	if q.workspace == "" || i == len(args) {
		return errComposition
	}
	for _, key := range []string{q.project, q.board} {
		if key != "" && T.Validate(T.WorkspaceProject{Key: T.WorkspaceKey(key), Label: "validation"}) != nil {
			return errComposition
		}
	}
	q.args = append([]string(nil), args[i:]...)
	for _, arg := range q.args {
		switch arg {
		case "--data", "--workspace", "--project", "--board", "--json", "--port", "--config", "--actor", "--native-owner", "--installation", "--incarnation", "--grant":
			return errComposition
		}
	}
	if jsonMode {
		q.args = append([]string{"--json"}, q.args...)
	}
	_, _, err := parseWorkspaceCommand(*q)
	return err
}

type workspaceCommand struct {
	family, action   string
	key, label, root string
	expectedFile     string
	expected         *T.WorkspaceIdentity
}

// Parse and validate before opening private state or contacting a service.
func parseWorkspaceCommand(q localInvocation) (workspaceCommand, []string, error) {
	args := q.args
	if len(args) > 0 && args[0] == "--json" {
		args = args[1:]
	}
	var c workspaceCommand
	if len(args) < 2 {
		return c, nil, errComposition
	}
	c.family, c.action = args[0], args[1]
	control := c.family == "project" || c.family == "board" && c.action != "read"
	if !control {
		if q.project == "" || q.board == "" {
			return c, nil, errComposition
		}
		// Fixed identities satisfy only the owning parser's selector syntax.
		// They never reach credential resolution, evidence or an HTTP request.
		grammar := append([]string{"--installation", "00000000-0000-4000-8000-000000000001", "--board", "00000000-0000-4000-8000-000000000002", "--incarnation", "00000000-0000-4000-8000-000000000003", "--grant", "00000000-0000-4000-8000-000000000004"}, args...)
		var err error
		if c.family == "attachment" || c.family == "export" {
			err = archive.ValidateInvocation(grammar)
		} else {
			err = core.ValidateInvocation(grammar)
		}
		if err != nil {
			return c, nil, errComposition
		}
		return c, args, nil
	}
	flags := map[string]string{}
	for i := 2; i < len(args); i += 2 {
		if i+1 >= len(args) || flags[args[i]] != "" || args[i+1] == "" || strings.ContainsAny(args[i+1], "\x00\r\n") {
			return c, nil, errComposition
		}
		switch args[i] {
		case "--key", "--label", "--root", "--expected-identity":
		default:
			return c, nil, errComposition
		}
		flags[args[i]] = args[i+1]
	}
	c.key, c.label, c.root = flags["--key"], flags["--label"], flags["--root"]
	switch c.family + " " + c.action {
	case "project list":
		if q.project != "" || q.board != "" || len(flags) != 0 {
			return c, nil, errComposition
		}
	case "project create":
		if q.project != "" || q.board != "" || len(flags) != 3 || c.key == "" || c.label == "" || c.root == "" {
			return c, nil, errComposition
		}
	case "board list":
		if q.board != "" || len(flags) != 0 {
			return c, nil, errComposition
		}
	case "board create", "board register":
		if q.project == "" || q.board != "" || c.key == "" || c.label == "" || c.root == "" || (c.action == "create" && flags["--expected-identity"] != "") {
			return c, nil, errComposition
		}
	case "board status":
		if q.project == "" || q.board == "" || c.key != "" || c.label != "" {
			return c, nil, errComposition
		}
	default:
		return c, nil, errComposition
	}
	if c.key != "" && T.Validate(T.WorkspaceProject{Key: T.WorkspaceKey(c.key), Label: T.WorkspaceLabel(c.label)}) != nil {
		return c, nil, errComposition
	}
	if c.root != "" {
		var err error
		c.root, err = filepath.Abs(filepath.FromSlash(c.root))
		if err != nil || !safeAbsolute(c.root) {
			return c, nil, errComposition
		}
	}
	c.expectedFile = flags["--expected-identity"]
	return c, nil, nil
}

func (c *workspaceCommand) readExpectedIdentity() error {
	if c.expectedFile != "" {
		f, err := os.Open(c.expectedFile)
		if err != nil {
			return errComposition
		}
		b, err := io.ReadAll(io.LimitReader(f, 4097))
		closeErr := f.Close()
		if err != nil || closeErr != nil || len(b) > 4096 {
			return errComposition
		}
		v, err := T.Decode[T.WorkspaceIdentity](b)
		if err != nil {
			return errComposition
		}
		c.expected = &v
	}
	return nil
}

func workspaceFailure(args []string, out, diagnostic io.Writer, err error) int {
	var failure *core.Failure
	if errors.As(err, &failure) {
		// This workspace-specific stale-configuration category is deliberately
		// outside the board-domain runner's fixed error vocabulary.
		if failure.Code == "CONFIG_CHANGED" {
			value := T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: "CONFIG_CHANGED", Message: "Workspace configuration changed; inspect it before retrying"}}
			b, e := T.Readable(value)
			if diagnostic != nil {
				_, _ = fmt.Fprintln(diagnostic, "CONFIG_CHANGED")
			}
			if e != nil || out == nil {
				return 1
			}
			if len(args) == 0 || args[0] != "--json" {
				if _, e = fmt.Fprintln(out, "Workspace configuration changed; inspect it before retrying"); e != nil {
					return 1
				}
			}
			if n, e := out.Write(b); e != nil || n != len(b) {
				return 1
			}
			return 2
		}
		return entryFailure(args, out, diagnostic, failure.Code, failure.Exit)
	}
	return entryFailure(args, out, diagnostic, "UNAVAILABLE", 3)
}

// A failed live selection cannot disprove an original submission. Recover only
// the already validated evidence identity; never contact its old source here.
func workspaceOriginalFailure(ctx context.Context, args []string, store evidence.Store, out, diagnostic io.Writer) int {
	jsonMode := len(args) > 0 && args[0] == "--json"
	command := args
	if jsonMode {
		command = command[1:]
	}
	fail := func() int { return entryFailure(args, out, diagnostic, "OUTCOME_UNKNOWN", 4) }
	if len(command) != 5 || command[3] != "--evidence" {
		return fail()
	}
	file := command[4]
	if !safeAbsolute(file) || filepath.Clean(file) != file {
		return fail()
	}
	if store != nil {
		original, err := store.Load(file)
		if err != nil || original.Identity().OpID != command[2] {
			return fail()
		}
	}
	var missing *core.Client
	value := missing.Lookup(ctx, store, file, diagnostic)
	b, err := T.Readable(value.Value())
	if err != nil {
		return fail()
	}
	if diagnostic != nil {
		_, _ = fmt.Fprintln(diagnostic, "OUTCOME_UNKNOWN")
	}
	if out != nil {
		if !jsonMode {
			_, _ = fmt.Fprintln(out, "Original outcome unknown")
		}
		_, _ = out.Write(b)
	}
	return 4
}

func runWorkspaceCLI(ctx context.Context, q localInvocation, in io.Reader, out, diagnostic io.Writer) int {
	if out == nil || diagnostic == nil {
		return entryFailure(q.args, out, diagnostic, "INVOCATION_OR_IO", 1)
	}
	c, domain, err := parseWorkspaceCommand(q)
	if err != nil {
		return entryFailure(q.args, out, diagnostic, "INVOCATION_OR_IO", 1)
	}
	if c.readExpectedIdentity() != nil {
		return entryFailure(q.args, out, diagnostic, "INVOCATION_OR_IO", 1)
	}
	if !retryWorkspaceCLICleanup() {
		return entryFailure(q.args, out, diagnostic, "UNAVAILABLE", 3)
	}
	originalLookup := len(domain) >= 2 && domain[0] == "operation" && domain[1] == "read"
	var originalStore evidence.Store
	if originalLookup {
		location, locateErr := W.Locate(q.workspace, T.WorkspaceKey(q.project), T.WorkspaceKey(q.board))
		if location != nil {
			defer finishWorkspaceCLI(location)
			if location.DataDir() != "" {
				originalStore, _ = evidence.NewWindowsStore(filepath.Join(location.DataDir(), "cli-evidence"))
			}
		}
		if locateErr != nil {
			return workspaceOriginalFailure(ctx, q.args, originalStore, out, diagnostic)
		}
	}
	fail := func(err error) int {
		if originalLookup {
			return workspaceOriginalFailure(ctx, q.args, originalStore, out, diagnostic)
		}
		return workspaceFailure(q.args, out, diagnostic, err)
	}
	client, err := W.Open(ctx, q.workspace)
	if err != nil {
		if client != nil {
			finishWorkspaceCLI(client)
		}
		return fail(err)
	}
	defer finishWorkspaceCLI(client)
	if domain != nil {
		selection, err := client.Select(ctx, T.WorkspaceKey(q.project), T.WorkspaceKey(q.board))
		if err != nil {
			return fail(err)
		}
		store, err := evidence.NewWindowsStore(filepath.Join(selection.DataDir(), "cli-evidence"))
		if err != nil {
			return entryFailure(q.args, out, diagnostic, "INVOCATION_OR_IO", 1)
		}
		s := selection.Selectors()
		prefix := []string{"--installation", s.InstallationID, "--board", s.BoardID, "--incarnation", s.Incarnation, "--grant", s.GrantID}
		if q.args[0] == "--json" {
			prefix = append([]string{"--json"}, prefix...)
		}
		args := append(prefix, domain...)
		if domain[0] == "attachment" || domain[0] == "export" {
			return (archive.Runner{Provider: selection.Provider(), Evidence: store, ProtectedRoots: []string{q.workspace, selection.DataDir()}}).Run(ctx, args, in, out, diagnostic)
		}
		return (core.Runner{Provider: selection.Provider(), Evidence: store}).Run(ctx, args, in, out, diagnostic)
	}
	var value any
	var result T.WorkspaceControlResult
	switch c.family + " " + c.action {
	case "project list":
		value, err = client.Projects(ctx)
	case "board list":
		value, err = client.Boards(ctx, T.WorkspaceKey(q.project))
	case "board status":
		request := T.WorkspaceStatusRequest{APIVersion: 1, ProjectKey: T.WorkspaceKey(q.project), BoardKey: T.WorkspaceKey(q.board), ExpectedIdentity: c.expected}
		if c.root != "" {
			root := T.WorkspacePath(c.root)
			request.DataDir = &root
		}
		result, err = client.Status(ctx, request)
		value = result
	default:
		// The connection reads the expected revision once and sends once.
		if c.family == "project" {
			result, err = client.CreateProject(ctx, T.WorkspaceProjectRequest{APIVersion: 1, Key: T.WorkspaceKey(c.key), Label: T.WorkspaceLabel(c.label), Root: T.WorkspacePath(c.root)})
		} else {
			result, err = client.CreateBoard(ctx, T.WorkspaceBoardRequest{APIVersion: 1, ProjectKey: T.WorkspaceKey(q.project), BoardKey: T.WorkspaceKey(c.key), Label: T.WorkspaceLabel(c.label), DataDir: T.WorkspacePath(c.root), Action: T.WorkspaceAction(c.action), ExpectedIdentity: c.expected})
		}
		value = result
	}
	if err != nil && result.Status != "UNKNOWN" {
		return workspaceFailure(q.args, out, diagnostic, err)
	}
	exit := 0
	if result.Status == "UNKNOWN" {
		exit = 4
	} else if result.Status == "unavailable" {
		exit = 3
	}
	outputFailure := 1
	if exit == 4 || (c.action == "create" || c.action == "register") && result.Status == "registered" {
		outputFailure = 4
	}
	if out == nil {
		return outputFailure
	}
	if q.args[0] == "--json" {
		b, e := T.Readable(value)
		if e != nil {
			return entryFailure(q.args, out, diagnostic, "INVOCATION_OR_IO", 1)
		}
		if n, e := out.Write(b); e != nil || n != len(b) {
			return outputFailure
		}
	} else {
		if e := writeWorkspaceText(out, value); e != nil {
			return outputFailure
		}
	}
	return exit
}

func writeWorkspaceText(out io.Writer, value any) error {
	switch v := value.(type) {
	case T.WorkspaceProjects:
		if _, e := fmt.Fprintf(out, "Projects (config revision %d)\n", v.ConfigRevision); e != nil {
			return e
		}
		for _, p := range v.Projects {
			if _, e := fmt.Fprintf(out, "%s\t%s\n", p.Key, strconv.QuoteToGraphic(string(p.Label))); e != nil {
				return e
			}
		}
	case T.WorkspaceBoards:
		if _, e := fmt.Fprintf(out, "Boards (config revision %d)\n", v.ConfigRevision); e != nil {
			return e
		}
		for _, b := range v.Boards {
			if _, e := fmt.Fprintf(out, "%s/%s\t%s\t%s\n", b.ProjectKey, b.BoardKey, strconv.QuoteToGraphic(string(b.Label)), b.Availability); e != nil {
				return e
			}
		}
	case T.WorkspaceControlResult:
		key := string(v.ProjectKey)
		if v.BoardKey != nil {
			key += "/" + string(*v.BoardKey)
		}
		if v.Status == "UNKNOWN" {
			_, e := fmt.Fprintf(out, "WORKSPACE UNKNOWN %s (last observed config revision %d; inspect status)\n", key, v.ConfigRevision)
			return e
		}
		_, e := fmt.Fprintf(out, "WORKSPACE %s %s (config revision %d)\n", v.Status, key, v.ConfigRevision)
		return e
	default:
		return errComposition
	}
	return nil
}
