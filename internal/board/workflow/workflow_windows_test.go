//go:build windows && amd64

package workflow

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"fmt"
	"os"
	"path/filepath"
	"strings"

	"testing"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	"golang.org/x/sys/windows"
)

const board = "10000000-0000-0000-0000-000000000001"
const inc = "20000000-0000-0000-0000-000000000001"
const actor = "30000000-0000-0000-0000-000000000001"
const grant = "30000000-0000-0000-0000-000000000002"
const grantee = "30000000-0000-0000-0000-000000000003"
const op = "50000000-0000-0000-0000-000000000001"
const op2 = "50000000-0000-0000-0000-000000000002"
const installation = "60000000-0000-0000-0000-000000000001"
const origin = "https://synthetic.localhost:47443"

var sid = schema.Identity{BoardID: board, Incarnation: inc}
var now = time.Date(2026, 10, 26, 0, 0, 1, 0, time.UTC)

func testContext(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}
func fixture(t *testing.T) (*writer.Engine, *S.Store) {
	t.Helper()
	root := os.Getenv("S01_TEST_ROOT")
	if root == "" {
		t.Skip("native synthetic fixtures require explicit private S01_TEST_ROOT")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("absolute private root required")
	}
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if strings.HasPrefix(strings.ToLower(filepath.Clean(root))+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("fixtures must be outside public repository")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	u, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	var nonce [8]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "fixture-"+hex.EncodeToString(nonce[:]))
	if err = windows.CreateDirectory(windows.StringToUTF16Ptr(p), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}); err != nil {
		t.Fatal(err)
	}
	s, err := S.Create(testContext(t), p, sid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	e := writer.New(s)

	raw(t, s, func(c *sql.Conn) error {
		for _, q := range []string{
			`INSERT INTO actor(incarnation,actor_id,kind,label,origin_board,origin_incarnation) VALUES('` + inc + `','` + actor + `','human','Synthetic','` + board + `','` + inc + `')`,
			`INSERT INTO grant_fact VALUES('` + inc + `','` + grant + `')`,
			`INSERT INTO grant_record VALUES('` + inc + `','` + grant + `','` + actor + `',1)`,
			`INSERT INTO grant_right VALUES('` + inc + `','` + grant + `','read-own-outcome')`,
		} {
			if _, err := c.ExecContext(testContext(t), q); err != nil {
				return err
			}
		}
		return nil
	})
	return e, s
}
func raw(t *testing.T, s *S.Store, fn func(*sql.Conn) error) {
	t.Helper()
	l, err := s.Writer(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err = l.Do(testContext(t), fn); err != nil {
		t.Fatal(err)
	}
}
func value(t *testing.T, s *S.Store, q string) int64 {
	t.Helper()
	l, err := s.Reader(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var n int64
	if err = l.Do(testContext(t), func(c *sql.Conn) error { return c.QueryRowContext(testContext(t), q).Scan(&n) }); err != nil {
		t.Fatal(err)
	}
	return n
}

func service(t *testing.T, s *S.Store) *grants.Service {
	t.Helper()
	raw(t, s, func(c *sql.Conn) error {
		for _, r := range []string{"create", "edit", "comment", "review-handoff", "approve"} {
			if _, err := c.ExecContext(testContext(t), "INSERT INTO grant_right VALUES(?,?,?)", inc, grant, r); err != nil {
				return err
			}
		}
		return nil
	})
	g, err := grants.New(installation, origin, func(ctx context.Context, r writer.Read, n time.Time) (grants.Credential, error) {
		// A test-only live verifier resolves actor from the current snapshot. It
		// supplies no rights; production callers use the G02 verifier instead.
		var a string
		err := r.Scan("SELECT actor_id FROM grant_record WHERE incarnation=? AND grant_id=?", []any{inc, grant}, &a)
		return grants.Credential{InstallationID: installation, Origin: origin, BoardID: board, Incarnation: inc, ActorID: a, GrantID: grant, Mode: "cli", IssuedAt: n.Add(-time.Minute), ExpiresAt: n.Add(time.Hour)}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func intent(t *testing.T, cmd, id string, rev, cardRev uint64, input map[string]any) contract.Intent {
	return intentFor(t, actor, cmd, id, rev, cardRev, input)
}
func intentFor(t *testing.T, who, cmd, id string, rev, cardRev uint64, input map[string]any) contract.Intent {
	t.Helper()
	var target any
	var cr any
	if id != "" {
		target = map[string]string{"cardId": id}
		cr = cardRev
	}
	var nonce [16]byte
	rand.Read(nonce[:])
	opID := fmt.Sprintf("%x-%x-%x-%x-%x", nonce[:4], nonce[4:6], nonce[6:8], nonce[8:10], nonce[10:])
	wire, _ := json.Marshal(map[string]any{"opId": opID, "incarnation": inc, "target": target, "expectedRevision": rev, "expectedCardRevision": cr, "input": input})
	i, err := T.FreezeMutation(wire, contract.ResolvedRoute{Command: contract.Command(cmd), BoardID: board, TargetID: id}, contract.AuthenticatedActor{ActorID: who})
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func submit(t *testing.T, e *writer.Engine, g *grants.Service, i contract.Intent, id, want string) writer.Result {
	t.Helper()
	cb := New(g).Callbacks(Route{board, inc, id})
	if i.Command() == "card.create" || i.Command() == "card.edit" || i.Command() == "comment.add" {
		cb = cards.New(g).Callbacks(cards.Route{BoardID: board, Incarnation: inc, CardID: id})
	}

	r := e.Submit(testContext(t), i, cb)
	if r.Terminal == nil || string(r.Terminal.Outcome) != want {
		t.Fatalf("%s expected %s (terminal=%v unknown=%v refused=%v)", i.Command(), want, r.Terminal, r.Unknown, r.Refused)
	}
	if err := T.Validate(*r.Terminal); err != nil {
		t.Fatal(err)
	}
	return r
}
func create(t *testing.T, e *writer.Engine, g *grants.Service, rev uint64) string {
	t.Helper()
	r := submit(t, e, g, intent(t, "card.create", "", rev, 0, map[string]any{"title": " 雪🌊 ", "description": "A\r\nB\n café"}), "", "committed")
	var result struct{ CardID string }
	json.Unmarshal(*r.Terminal.Result, &result)
	return result.CardID
}
func resultID(t *testing.T, r writer.Result, key string) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(*r.Terminal.Result, &m); err != nil {
		t.Fatal(err)
	}
	if m[key] == "" {
		t.Fatal("missing result ID")
	}
	return m[key]
}
func issue(t *testing.T, e *writer.Engine, db *S.Store, g *grants.Service, cmd, id string, input map[string]any, want string) writer.Result {
	t.Helper()
	rev := uint64(value(t, db, "SELECT revision FROM board"))
	cr := uint64(value(t, db, "SELECT revision FROM card WHERE card_id='"+id+"'"))
	return submit(t, e, g, intent(t, cmd, id, rev, cr, input), id, want)
}
func code(t *testing.T, r writer.Result, want string) {
	t.Helper()
	if r.Terminal == nil || r.Terminal.Code != want {
		t.Fatalf("expected %s: %#v", want, r.Terminal)
	}
}
func execSQL(t *testing.T, db *S.Store, q string, args ...any) {
	t.Helper()
	raw(t, db, func(c *sql.Conn) error { _, err := c.ExecContext(testContext(t), q, args...); return err })
}

func TestGraphOwnerHistoryAndReplay(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	a := create(t, e, g, 0)
	b := create(t, e, g, 1)
	c := create(t, e, g, 2)
	i := intent(t, "link.add", a, 3, 1, map[string]any{"kind": "blocking", "targetCardId": b})
	r := submit(t, e, g, i, a, "committed")
	link := resultID(t, r, "linkId")
	submit(t, e, g, i, a, "committed")
	if value(t, db, "SELECT revision FROM board") != 4 || value(t, db, "SELECT revision FROM card WHERE card_id='"+a+"'") != 2 || value(t, db, "SELECT revision FROM card WHERE card_id='"+b+"'") != 1 || value(t, db, "SELECT count(*) FROM link") != 1 {
		t.Fatal("owner-only revision or replay")
	}
	if value(t, db, "SELECT count(*) FROM entity WHERE kind='link' AND creator_id='"+actor+"' AND origin_board='"+board+"' AND origin_incarnation='"+inc+"'") != 1 {
		t.Fatal("immutable attributed link")
	}
	issue(t, e, db, g, "link.add", b, map[string]any{"kind": "blocking", "targetCardId": c}, "committed")
	code(t, issue(t, e, db, g, "link.add", c, map[string]any{"kind": "blocking", "targetCardId": a}, "not_committed"), "BLOCKING_CYCLE")
	code(t, issue(t, e, db, g, "link.add", a, map[string]any{"kind": "informational", "targetCardId": a}, "not_committed"), "SELF_LINK")
	code(t, issue(t, e, db, g, "link.remove", b, map[string]any{"linkId": link}, "not_committed"), "INVALID_INPUT")
	issue(t, e, db, g, "link.remove", a, map[string]any{"linkId": link}, "committed")
	if value(t, db, "SELECT count(*) FROM link WHERE active=0") != 1 || value(t, db, "SELECT count(*) FROM revision_reference WHERE kind='link' AND entity_id='"+link+"'") != 1 || value(t, db, "SELECT count(*) FROM current_reference WHERE entity_id='"+link+"'") != 0 {
		t.Fatal("removed identity/history")
	}
	issue(t, e, db, g, "link.add", c, map[string]any{"kind": "blocking", "targetCardId": a}, "committed")                                // removed edge no longer closes cycle
	issue(t, e, db, g, "link.add", a, map[string]any{"kind": "informational", "targetCardId": c}, "committed")                           // informational cycle is permitted
	code(t, issue(t, e, db, g, "link.add", a, map[string]any{"kind": "blocking", "targetCardId": b}, "not_committed"), "BLOCKING_CYCLE") // current graph now closes a new cycle
	if value(t, db, "SELECT count(*) FROM link WHERE card_id='"+a+"' AND kind='blocking'") != 1 {
		t.Fatal("cycle admission ignored current graph")
	}
	if value(t, db, "SELECT count(*) FROM card WHERE priority<>'P3'") != 0 {
		t.Fatal("graph reprioritized cards")
	}
}

func TestApprovalLifecycleAndCompleted(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	a := create(t, e, g, 0)
	code(t, issue(t, e, db, g, "review-handoff", a, map[string]any{}, "not_committed"), "INVALID_TRANSITION")
	code(t, issue(t, e, db, g, "approve", a, map[string]any{"policyVersion": 1}, "not_committed"), "INVALID_TRANSITION")
	issue(t, e, db, g, "card.edit", a, map[string]any{"lane": "In Progress"}, "committed")
	i := intent(t, "review-handoff", a, 2, 2, map[string]any{})
	submit(t, e, g, i, a, "committed")
	submit(t, e, g, i, a, "committed")
	if value(t, db, "SELECT count(*) FROM approval") != 0 {
		t.Fatal("handoff conferred approval")
	}
	approvalIntent := intent(t, "approve", a, 3, 3, map[string]any{"policyVersion": 1})
	r := submit(t, e, g, approvalIntent, a, "committed")
	replayed := submit(t, e, g, approvalIntent, a, "committed")
	if string(*r.Terminal.Result) != string(*replayed.Terminal.Result) || r.Terminal.OpID != replayed.Terminal.OpID {
		t.Fatal("approval replay changed recorded identity")
	}
	approval := resultID(t, r, "approvalId")
	if value(t, db, "SELECT count(*) FROM card WHERE lane='Completed' AND revision=4 AND current_approval_id='"+approval+"'") != 1 || value(t, db, "SELECT count(*) FROM approval WHERE reviewed_card_revision=3 AND reviewed_board_revision=3 AND policy_version=1 AND actor_id='"+actor+"'") != 1 {
		t.Fatal("approval did not bind reviewed pre-approval state")
	}
	for _, cmd := range []string{"review-handoff", "approve", "link.add", "link.remove", "comment.add", "card.edit"} {
		t.Run(cmd, func(t *testing.T) {
			input := map[string]any{}
			switch cmd {
			case "approve":
				input["policyVersion"] = 1
			case "link.add":
				input["kind"] = "informational"
				input["targetCardId"] = a
			case "link.remove":
				input["linkId"] = op
			case "comment.add":
				input["text"] = "cannot mutate"
			case "card.edit":
				input["title"] = "cannot mutate"
			}
			code(t, issue(t, e, db, g, cmd, a, input, "not_committed"), "COMPLETED")
		})
	}
	if value(t, db, "SELECT revision FROM board") != 4 || value(t, db, "SELECT count(*) FROM activity") != 4 || value(t, db, "SELECT count(*) FROM card_revision") != 3 {
		t.Fatal("completed refusal changed domain")
	}
}

func TestPrerequisiteComposedFlow(t *testing.T) {
	for _, kind := range []string{"blocking", "informational", "removed"} {
		t.Run(kind, func(t *testing.T) {
			e, db := fixture(t)
			g := service(t, db)
			owner := create(t, e, g, 0)
			peer := create(t, e, g, 1)
			k := kind
			if k == "removed" {
				k = "blocking"
			}
			r := issue(t, e, db, g, "link.add", owner, map[string]any{"kind": k, "targetCardId": peer}, "committed")
			if kind == "removed" {
				issue(t, e, db, g, "link.remove", owner, map[string]any{"linkId": resultID(t, r, "linkId")}, "committed")
			}
			if kind == "blocking" {
				code(t, issue(t, e, db, g, "card.edit", owner, map[string]any{"lane": "In Progress"}, "not_committed"), "PREREQUISITE_NOT_APPROVED")
			} else {
				issue(t, e, db, g, "card.edit", owner, map[string]any{"lane": "In Progress"}, "committed")
			}
			issue(t, e, db, g, "card.edit", peer, map[string]any{"lane": "In Progress"}, "committed")
			issue(t, e, db, g, "review-handoff", peer, map[string]any{}, "committed")
			issue(t, e, db, g, "approve", peer, map[string]any{"policyVersion": 1}, "committed")
			if kind == "blocking" {
				issue(t, e, db, g, "card.edit", owner, map[string]any{"lane": "In Progress"}, "committed")
			}
		})
	}
}

func TestWrongRoutesAndCurrentRights(t *testing.T) {
	for _, mode := range []string{"owner-route", "board-route", "incarnation-route", "missing-peer", "missing-link", "revoked", "edit-right", "handoff-right", "approve-right", "automation"} {
		t.Run(mode, func(t *testing.T) {
			e, db := fixture(t)
			g := service(t, db)
			a := create(t, e, g, 0)
			b := create(t, e, g, 1)
			cmd := "link.add"
			input := map[string]any{"kind": "blocking", "targetCardId": b}
			want := "INVALID_INPUT"
			route := Route{board, inc, a}
			switch mode {
			case "owner-route":
				route.CardID = b
			case "board-route":
				route.BoardID = op
			case "incarnation-route":
				route.Incarnation = op
			case "missing-peer":
				input["targetCardId"] = op
				want = "NOT_FOUND"
			case "missing-link":
				cmd = "link.remove"
				input = map[string]any{"linkId": op}
				want = "NOT_FOUND"
			case "revoked":
				execSQL(t, db, "UPDATE grant_record SET enabled=0")
				want = ""
			case "edit-right":
				execSQL(t, db, "DELETE FROM grant_right WHERE right_name='edit'")
				want = "FORBIDDEN"
			case "handoff-right":
				cmd = "review-handoff"
				input = map[string]any{}
				execSQL(t, db, "DELETE FROM grant_right WHERE right_name='review-handoff'")
				want = "FORBIDDEN"
			case "approve-right":
				cmd = "approve"
				input = map[string]any{"policyVersion": 1}
				execSQL(t, db, "DELETE FROM grant_right WHERE right_name='approve'")
				want = "FORBIDDEN"
			case "automation":
				cmd = "approve"
				input = map[string]any{"policyVersion": 1}
				execSQL(t, db, "INSERT INTO actor(incarnation,actor_id,kind,label,origin_board,origin_incarnation) VALUES(?,?,'automation','Synthetic automation',?,?)", inc, grantee, board, inc)
				execSQL(t, db, "UPDATE grant_record SET actor_id=?", grantee)
				want = "" // invalid automation rights fail current G01 authority
			}
			who := actor
			if mode == "automation" {
				who = grantee
			}
			i := intentFor(t, who, cmd, a, 2, 1, input)
			r := e.Submit(testContext(t), i, New(g).Callbacks(route))
			if want != "" {
				code(t, r, want)
			} else if r.Unknown == nil && r.Refused == nil {
				t.Fatal("revoked/automation authority admitted")
			}
			if value(t, db, "SELECT revision FROM board") != 2 || value(t, db, "SELECT count(*) FROM link") != 0 || value(t, db, "SELECT count(*) FROM approval") != 0 {
				t.Fatal("denial leaked effects")
			}
		})
	}
}

func TestApprovalRevocationSerializedBeforeAdmission(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	a := create(t, e, g, 0)
	issue(t, e, db, g, "card.edit", a, map[string]any{"lane": "In Progress"}, "committed")
	issue(t, e, db, g, "review-handoff", a, map[string]any{}, "committed")
	// Hold the actual S01 writer while an already-frozen approval is queued.
	i := intent(t, "approve", a, 3, 3, map[string]any{"policyVersion": 1})
	l, err := db.Writer(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan writer.Result, 1)
	go func() { ch <- e.Submit(testContext(t), i, New(g).Callbacks(Route{board, inc, a})) }()
	if err = l.Do(testContext(t), func(c *sql.Conn) error {
		_, err := c.ExecContext(testContext(t), "DELETE FROM grant_right WHERE right_name='approve'")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	code(t, <-ch, "FORBIDDEN")
	if value(t, db, "SELECT count(*) FROM approval") != 0 || value(t, db, "SELECT revision FROM board") != 3 {
		t.Fatal("queued stale approval escaped revocation")
	}
}

func TestExactApprovalRevisionsAndRefusalFence(t *testing.T) {
	for _, mode := range []string{"board", "card", "old-state"} {
		t.Run(mode, func(t *testing.T) {
			e, db := fixture(t)
			g := service(t, db)
			a := create(t, e, g, 0)
			issue(t, e, db, g, "card.edit", a, map[string]any{"lane": "In Progress"}, "committed")
			issue(t, e, db, g, "review-handoff", a, map[string]any{}, "committed")
			br, cr := uint64(3), uint64(3)
			if mode == "board" {
				br = 2
			}
			if mode == "card" {
				cr = 2
			}
			if mode == "old-state" {
				execSQL(t, db, "UPDATE card SET lane='In Progress'")
			}
			i := intent(t, "approve", a, br, cr, map[string]any{"policyVersion": 1})
			r := submit(t, e, g, i, a, "not_committed")
			if mode == "old-state" {
				code(t, r, "INVALID_TRANSITION")
			} else {
				code(t, r, "REVISION_CONFLICT")
			}
			issue(t, e, db, g, "comment.add", a, map[string]any{"text": "new content"}, "committed")
			submit(t, e, g, i, a, "not_committed")
			if value(t, db, "SELECT count(*) FROM approval") != 0 || value(t, db, "SELECT revision FROM board") != 4 {
				t.Fatal("terminal refusal identity was replayed")
			}
		})
	}
}

func TestEveryCommandPreservesFullPriorSnapshot(t *testing.T) {
	for _, cmd := range []string{"link.add", "link.remove", "review-handoff", "approve"} {
		t.Run(cmd, func(t *testing.T) {
			e, db := fixture(t)
			g := service(t, db)
			a := create(t, e, g, 0)
			b := create(t, e, g, 1)
			issue(t, e, db, g, "comment.add", a, map[string]any{"text": "immutable comment"}, "committed")
			r := issue(t, e, db, g, "link.add", a, map[string]any{"kind": "informational", "targetCardId": b}, "committed")
			link := resultID(t, r, "linkId")
			issue(t, e, db, g, "card.edit", a, map[string]any{"lane": "In Progress"}, "committed")
			if cmd == "approve" {
				issue(t, e, db, g, "review-handoff", a, map[string]any{}, "committed")
			}
			oldApproval := "80000000-0000-0000-0000-000000000001"
			attachment := "90000000-0000-0000-0000-000000000001"
			digest := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
			execSQL(t, db, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'approval',?,?,?,?)", inc, oldApproval, a, board, inc)
			execSQL(t, db, "INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,1,1,1,'2026-10-26T00:00:00Z')", inc, oldApproval, a, actor)
			execSQL(t, db, "UPDATE card SET current_approval_id=? WHERE card_id=?", oldApproval, a)
			execSQL(t, db, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'attachment',?,?,?,?)", inc, attachment, a, board, inc)
			execSQL(t, db, "INSERT INTO blob VALUES(?,?,0,?)", inc, digest, []byte{})
			execSQL(t, db, "INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) VALUES(?,?,?,'Synthetic','text/plain',?,0,'2026-10-26T00:00:00Z')", inc, attachment, a, digest)
			execSQL(t, db, "INSERT INTO current_reference VALUES(?,?,'attachment',?)", inc, a, attachment)
			execSQL(t, db, "INSERT INTO current_attachment_pin VALUES(?,?,?,0)", inc, attachment, digest)
			cr := value(t, db, "SELECT revision FROM card WHERE card_id='"+a+"'")
			br := value(t, db, "SELECT revision FROM board")
			events := value(t, db, "SELECT next_event FROM board")
			input := map[string]any{}
			switch cmd {
			case "link.add":
				input = map[string]any{"kind": "blocking", "targetCardId": b}
			case "link.remove":
				input["linkId"] = link
			case "approve":
				input["policyVersion"] = 1
			}
			r = issue(t, e, db, g, cmd, a, input, "committed")
			var prior A.SnapshotState
			l, err := db.Reader(testContext(t))
			if err != nil {
				t.Fatal(err)
			}
			err = l.Do(testContext(t), func(c *sql.Conn) error {
				var state string
				err := c.QueryRowContext(testContext(t), "SELECT state FROM card_revision WHERE card_id=? AND card_revision=?", a, cr).Scan(&state)
				if err != nil {
					return err
				}
				return json.Unmarshal([]byte(state), &prior)
			})
			l.Close()
			if err != nil {
				t.Fatal(err)
			}
			if prior.Title != " 雪🌊 " || prior.Description != "A\r\nB\n café" || prior.Priority != "P3" || prior.Archived || prior.CurrentApprovalID == nil || string(*prior.CurrentApprovalID) != oldApproval || (cmd == "approve" && prior.Lane != "Ready for Review") || (cmd != "approve" && prior.Lane != "In Progress") {
				t.Fatal("incomplete prior scalar/approval snapshot")
			}
			if value(t, db, fmt.Sprintf("SELECT count(*) FROM revision_reference WHERE card_id='%s' AND card_revision=%d", a, cr)) != 4 || value(t, db, fmt.Sprintf("SELECT count(*) FROM history_blob_pin WHERE card_id='%s' AND card_revision=%d", a, cr)) != 1 || value(t, db, "SELECT count(*) FROM current_attachment_pin") != 1 {
				t.Fatal("prior reference or byte pin omission")
			}
			if value(t, db, "SELECT revision FROM board") != br+1 || value(t, db, "SELECT next_event FROM board") != events+1 || value(t, db, "SELECT revision FROM card WHERE card_id='"+a+"'") != cr+1 {
				t.Fatal("counters did not increment once")
			}
			if value(t, db, fmt.Sprintf("SELECT count(*) FROM card_revision s JOIN operation o ON o.incarnation=s.incarnation AND o.origin_board=s.origin_board AND o.origin_incarnation=s.origin_incarnation AND o.actor_id=s.actor_id AND o.terminal_at=s.committed_at AND o.committed_card_revision=s.card_revision JOIN operation_reference r ON r.incarnation=o.incarnation AND r.origin_board=o.origin_board AND r.origin_incarnation=o.origin_incarnation AND r.op_id=o.op_id AND r.entity_id=s.card_id AND r.revision=s.card_revision WHERE s.card_id='%s' AND s.card_revision=%d AND o.terminal_class='COMMITTED'", a, cr)) != 1 {
				t.Fatal("original revision provenance changed")
			}
			if cmd != "approve" && value(t, db, "SELECT count(*) FROM card WHERE card_id='"+a+"' AND current_approval_id IS NULL") != 1 {
				t.Fatal("card mutation retained current approval")
			}
			if value(t, db, "SELECT count(*) FROM approval WHERE approval_id='"+oldApproval+"'") != 1 || len(r.Terminal.ResultRefs) < 1 {
				t.Fatal("historical approval or committed result facts lost")
			}
		})
	}
}

func TestCountersAndPostEffectRollback(t *testing.T) {
	for _, mode := range []string{"event", "card", "board", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			e, db := fixture(t)
			g := service(t, db)
			a := create(t, e, g, 0)
			b := create(t, e, g, 1)
			br, cr := uint64(2), uint64(1)
			switch mode {
			case "event":
				execSQL(t, db, "UPDATE board SET next_event=?", contract.MaxInteger)
			case "card":
				br = contract.MaxInteger
				execSQL(t, db, "UPDATE board SET revision=?", br)
				cr = contract.MaxInteger
				execSQL(t, db, "UPDATE card SET revision=? WHERE card_id=?", cr, a)
			case "board":
				br = contract.MaxInteger
				execSQL(t, db, "UPDATE board SET revision=?", br)
			}
			i := intent(t, "link.add", a, br, cr, map[string]any{"kind": "blocking", "targetCardId": b})
			cb := New(g).Callbacks(Route{board, inc, a})
			if mode == "rollback" {
				apply := cb.Apply
				cb.Apply = func(ctx context.Context, w writer.Write, i contract.Intent, at A.Timestamp) (writer.Effect, error) {
					_, err := apply(ctx, w, i, at)
					if err != nil {
						return writer.Effect{}, err
					}
					return writer.Effect{}, errors.New("synthetic post-effect failure")
				}
			}
			r := e.Submit(testContext(t), i, cb)
			if mode == "rollback" {
				if r.Unknown == nil {
					t.Fatal("application failure inferred noncommit")
				}
			} else {
				code(t, r, "COUNTER_OVERFLOW")
			}
			if value(t, db, "SELECT revision FROM board") != int64(br) || value(t, db, "SELECT revision FROM card WHERE card_id='"+a+"'") != int64(cr) || value(t, db, "SELECT count(*) FROM link") != 0 || value(t, db, "SELECT count(*) FROM card_revision") != 0 || value(t, db, "SELECT count(*) FROM activity") != 2 {
				t.Fatal("failure leaked partial state")
			}
		})
	}
}

func TestCompactSourceProvenanceAndMissingProvenance(t *testing.T) {
	for _, mode := range []string{"compact-source", "missing", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			e, db := fixture(t)
			g := service(t, db)
			a := create(t, e, g, 0)
			b := create(t, e, g, 1)
			execSQL(t, db, "UPDATE board SET revision=9")
			execSQL(t, db, "UPDATE card SET revision=2 WHERE card_id=?", a)
			sourceBoard := "10000000-0000-0000-0000-000000000009"
			sourceInc := "20000000-0000-0000-0000-000000000009"
			if mode != "missing" {
				execSQL(t, db, `INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision,removed_at,removal_reason,removal_revision) VALUES(?,?,?,?,0,?,?,'card.edit','card',?,4,1,'COMMITTED','OK','2026-10-25T01:02:03Z',5,2,'2026-10-25T02:00:00Z','expired',6)`, inc, sourceBoard, sourceInc, op2, actor, strings.Repeat("a", 64), a)
				execSQL(t, db, "INSERT INTO operation_reference VALUES(?,?,?,?,'card',?,2)", inc, sourceBoard, sourceInc, op2, a)
				if mode == "ambiguous" {
					execSQL(t, db, `INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision,removed_at,removal_reason,removal_revision) VALUES(?,?,?,?,0,?,?,'card.edit','card',?,4,1,'COMMITTED','OK','2026-10-25T01:02:03Z',5,2,'2026-10-25T02:00:00Z','expired',6)`, inc, sourceBoard, sourceInc, op, actor, strings.Repeat("b", 64), a)
					execSQL(t, db, "INSERT INTO operation_reference VALUES(?,?,?,?,'card',?,2)", inc, sourceBoard, sourceInc, op, a)
				}
			}
			i := intent(t, "link.add", a, 9, 2, map[string]any{"kind": "blocking", "targetCardId": b})
			if mode == "compact-source" {
				submit(t, e, g, i, a, "committed")
				if value(t, db, "SELECT count(*) FROM card_revision WHERE origin_board='"+sourceBoard+"' AND origin_incarnation='"+sourceInc+"' AND card_revision=2 AND committed_at='2026-10-25T01:02:03Z'") != 1 {
					t.Fatal("compacted source provenance rewritten")
				}
			} else {
				r := e.Submit(testContext(t), i, New(g).Callbacks(Route{board, inc, a}))
				if r.Unknown == nil || value(t, db, "SELECT revision FROM board") != 9 || value(t, db, "SELECT count(*) FROM link") != 0 || value(t, db, "SELECT count(*) FROM card_revision") != 0 {
					t.Fatal("invented provenance")
				}
			}
		})
	}
}

func TestUnsupportedApprovalPolicyRejectedByContract(t *testing.T) {
	// Policy version 1 is frozen by both C01 and schema 3. Version 2 cannot reach
	// domain admission; no policy migration or arbitrary accepted-version fallback.
	e, db := fixture(t)
	g := service(t, db)
	a := create(t, e, g, 0)
	wire, _ := json.Marshal(map[string]any{"opId": op, "incarnation": inc, "target": map[string]string{"cardId": a}, "expectedRevision": 1, "expectedCardRevision": 1, "input": map[string]int{"policyVersion": 2}})
	if _, err := T.FreezeMutation(wire, contract.ResolvedRoute{Command: "approve", BoardID: board, TargetID: a}, contract.AuthenticatedActor{ActorID: actor}); err == nil {
		t.Fatal("unfrozen approval policy accepted")
	}
	if value(t, db, "SELECT count(*) FROM operation") != 1 {
		t.Fatal("invalid wire input gained ledger identity")
	}
}

func TestDuplicateAndInactiveRefusalFence(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	a := create(t, e, g, 0)
	b := create(t, e, g, 1)
	r := issue(t, e, db, g, "link.add", a, map[string]any{"kind": "blocking", "targetCardId": b}, "committed")
	link := resultID(t, r, "linkId")
	i := intent(t, "link.add", a, 3, 2, map[string]any{"kind": "blocking", "targetCardId": b})
	code(t, submit(t, e, g, i, a, "not_committed"), "LINK_ALREADY_EXISTS")
	if value(t, db, "SELECT revision FROM board") != 3 || value(t, db, "SELECT count(*) FROM card_revision") != 1 || value(t, db, "SELECT next_event FROM board") != 4 {
		t.Fatal("duplicate add changed domain")
	}
	issue(t, e, db, g, "link.add", a, map[string]any{"kind": "informational", "targetCardId": b}, "committed") // active uniqueness is typed
	issue(t, e, db, g, "link.remove", a, map[string]any{"linkId": link}, "committed")
	removed := intent(t, "link.remove", a, 5, 4, map[string]any{"linkId": link})
	code(t, submit(t, e, g, removed, a, "not_committed"), "NOT_FOUND")
	issue(t, e, db, g, "link.add", a, map[string]any{"kind": "blocking", "targetCardId": b}, "committed")
	submit(t, e, g, i, a, "not_committed")
	submit(t, e, g, removed, a, "not_committed")
	if value(t, db, "SELECT revision FROM board") != 6 || value(t, db, "SELECT count(*) FROM link WHERE active=1") != 2 || value(t, db, "SELECT count(*) FROM link WHERE active=0") != 1 {
		t.Fatal("delayed refused identity mutated graph")
	}
}

func TestCurrentPrerequisiteApprovalStillRequired(t *testing.T) {
	for _, mode := range []string{"approved", "unapproved", "archived", "stale-approval", "automation-approval"} {
		t.Run(mode, func(t *testing.T) {
			e, db := fixture(t)
			g := service(t, db)
			a := create(t, e, g, 0)
			b := create(t, e, g, 1)
			issue(t, e, db, g, "card.edit", b, map[string]any{"lane": "In Progress"}, "committed")
			issue(t, e, db, g, "review-handoff", b, map[string]any{}, "committed")
			issue(t, e, db, g, "approve", b, map[string]any{"policyVersion": 1}, "committed")
			issue(t, e, db, g, "link.add", a, map[string]any{"kind": "blocking", "targetCardId": b}, "committed")
			switch mode {
			case "unapproved":
				execSQL(t, db, "UPDATE card SET current_approval_id=NULL WHERE card_id=?", b)
			case "archived":
				execSQL(t, db, "UPDATE card SET archived=1 WHERE card_id=?", b)
			case "stale-approval":
				execSQL(t, db, "UPDATE card SET revision=5 WHERE card_id=?", b)
			case "automation-approval":
				// Synthetic historical corruption cannot grant present start authority.
				execSQL(t, db, "INSERT INTO actor(incarnation,actor_id,kind,label,origin_board,origin_incarnation) VALUES(?,?,'automation','Synthetic automation',?,?)", inc, grantee, board, inc)
				execSQL(t, db, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'approval',?,?,?,?)", inc, op, b, board, inc)
				execSQL(t, db, "INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,3,4,1,'2026-10-26T00:00:00Z')", inc, op, b, grantee)
				execSQL(t, db, "UPDATE card SET current_approval_id=? WHERE card_id=?", op, b)
			}
			want := "not_committed"
			if mode == "approved" {
				want = "committed"
			}
			r := issue(t, e, db, g, "card.edit", a, map[string]any{"lane": "In Progress"}, want)
			if mode != "approved" {
				code(t, r, "PREREQUISITE_NOT_APPROVED")
			}
		})
	}
}

func TestHandoffDoesNotGrantApproval(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	a := create(t, e, g, 0)
	execSQL(t, db, "DELETE FROM grant_right WHERE right_name='approve'")
	issue(t, e, db, g, "card.edit", a, map[string]any{"lane": "In Progress"}, "committed")
	issue(t, e, db, g, "review-handoff", a, map[string]any{}, "committed")
	code(t, issue(t, e, db, g, "approve", a, map[string]any{"policyVersion": 1}, "not_committed"), "FORBIDDEN")
	if value(t, db, "SELECT count(*) FROM grant_right WHERE right_name='approve'") != 0 || value(t, db, "SELECT count(*) FROM approval") != 0 {
		t.Fatal("handoff granted approval authority")
	}
}

func TestNoncurrentLinkAndDuplicatePreserveApproval(t *testing.T) {
	for _, mode := range []string{"noncurrent", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			e, db := fixture(t)
			g := service(t, db)
			a := create(t, e, g, 0)
			b := create(t, e, g, 1)
			r := issue(t, e, db, g, "link.add", a, map[string]any{"kind": "informational", "targetCardId": b}, "committed")
			link := resultID(t, r, "linkId")
			oldApproval := "80000000-0000-0000-0000-000000000001"
			execSQL(t, db, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'approval',?,?,?,?)", inc, oldApproval, a, board, inc)
			execSQL(t, db, "INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,1,1,1,'2026-10-26T00:00:00Z')", inc, oldApproval, a, actor)
			execSQL(t, db, "UPDATE card SET current_approval_id=? WHERE card_id=?", oldApproval, a)
			if mode == "noncurrent" {
				execSQL(t, db, "DELETE FROM current_reference WHERE kind='link' AND entity_id=?", link)
				code(t, issue(t, e, db, g, "link.remove", a, map[string]any{"linkId": link}, "not_committed"), "NOT_FOUND")
			} else {
				code(t, issue(t, e, db, g, "link.add", a, map[string]any{"kind": "informational", "targetCardId": b}, "not_committed"), "LINK_ALREADY_EXISTS")
			}
			if value(t, db, "SELECT revision FROM board") != 3 || value(t, db, "SELECT revision FROM card WHERE card_id='"+a+"'") != 2 || value(t, db, "SELECT count(*) FROM card_revision") != 1 || value(t, db, "SELECT next_event FROM board") != 4 || value(t, db, "SELECT count(*) FROM card WHERE card_id='"+a+"' AND current_approval_id='"+oldApproval+"'") != 1 || value(t, db, "SELECT count(*) FROM link WHERE active=1") != 1 {
				t.Fatal("refusal changed domain state or approval")
			}
		})
	}
}

func TestArchivedTransitionsPreserveFlagAndRequireCurrentRights(t *testing.T) {
	for _, cmd := range []string{"review-handoff", "approve"} {
		for _, denied := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-denied-%t", cmd, denied), func(t *testing.T) {
				e, db := fixture(t)
				g := service(t, db)
				a := create(t, e, g, 0)
				issue(t, e, db, g, "card.edit", a, map[string]any{"lane": "In Progress"}, "committed")
				execSQL(t, db, "UPDATE card SET archived=1 WHERE card_id=?", a)
				input := map[string]any{}
				lane := "Ready for Review"
				br := int64(2)
				if cmd == "approve" {
					issue(t, e, db, g, "review-handoff", a, map[string]any{}, "committed")
					input["policyVersion"] = 1
					lane = "Completed"
					br = 3
				}
				want := "committed"
				if denied {
					execSQL(t, db, "DELETE FROM grant_right WHERE right_name=?", cmd)
					want = "not_committed"
				}
				r := issue(t, e, db, g, cmd, a, input, want)
				if denied {
					code(t, r, "FORBIDDEN")
					if value(t, db, "SELECT revision FROM board") != br {
						t.Fatal("denied transition changed revision")
					}
				} else if value(t, db, "SELECT count(*) FROM card WHERE lane='"+lane+"' AND archived=1") != 1 {
					t.Fatal("transition changed archived flag")
				}
			})
		}
	}
}
