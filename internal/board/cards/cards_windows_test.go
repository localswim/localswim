//go:build windows && amd64

package cards

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"

	"fmt"
	"os"
	"path/filepath"
	"strings"

	"testing"
	"time"
	"unsafe"

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
	p := filepath.Join(root, strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")+"-"+hex.EncodeToString(nonce[:]))
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

func service(t *testing.T, s *S.Store) *Service {
	t.Helper()
	raw(t, s, func(c *sql.Conn) error {
		for _, r := range []string{"create", "edit", "comment"} {
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
	return New(g)
}
func intent(t *testing.T, cmd, id string, rev, cardRev uint64, input map[string]any) contract.Intent {
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
	i, err := T.FreezeMutation(wire, contract.ResolvedRoute{Command: contract.Command(cmd), BoardID: board, TargetID: id}, contract.AuthenticatedActor{ActorID: actor})
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func submit(t *testing.T, e *writer.Engine, s *Service, i contract.Intent, id, want string) writer.Result {
	t.Helper()
	r := e.Submit(testContext(t), i, s.Callbacks(Route{board, inc, id}))
	if r.Terminal == nil || string(r.Terminal.Outcome) != want {
		t.Fatalf("expected %s (terminal=%v unknown=%v refused=%v)", want, r.Terminal != nil, r.Unknown != nil, r.Refused != nil)
	}
	if err := T.Validate(*r.Terminal); err != nil {
		t.Fatal(err)
	}
	return r
}
func create(t *testing.T, e *writer.Engine, s *Service, rev uint64) string {
	t.Helper()
	r := submit(t, e, s, intent(t, "card.create", "", rev, 0, map[string]any{"title": " 雪🌊 ", "description": "A\r\nB\n café"}), "", "committed")
	var result struct {
		CardID string `json:"cardId"`
	}
	json.Unmarshal(*r.Terminal.Result, &result)
	return result.CardID
}
func TestExamplesAndExactPriorState(t *testing.T) {
	e, db := fixture(t)
	s := service(t, db)
	id := create(t, e, s, 0)
	if value(t, db, "SELECT count(*) FROM card WHERE revision=1 AND priority='P3' AND lane='Ready for Work' AND title=' 雪🌊 '") != 1 {
		t.Fatal("create defaults/text")
	}
	submit(t, e, s, intent(t, "card.edit", id, 1, 1, map[string]any{"description": ""}), id, "committed")
	submit(t, e, s, intent(t, "comment.add", id, 2, 2, map[string]any{"text": "  immutable 🐬\r\n "}), id, "committed")
	submit(t, e, s, intent(t, "card.edit", id, 3, 3, map[string]any{"title": "新 title", "priority": "P1"}), id, "committed")
	if value(t, db, "SELECT count(*) FROM card_revision") != 3 || value(t, db, "SELECT count(*) FROM activity") != 4 || value(t, db, "SELECT count(*) FROM operation_full") != 4 {
		t.Fatal("atomic records missing")
	}
	if value(t, db, "SELECT count(*) FROM card_revision WHERE card_revision=1 AND json_extract(state,'$.description')='A'||char(13)||char(10)||'B'||char(10)||' café'") != 1 {
		t.Fatal("prior prose")
	}
	if value(t, db, "SELECT count(*) FROM revision_reference WHERE kind='comment' AND card_revision=3") != 1 {
		t.Fatal("prior comment reference")
	}
	if value(t, db, "SELECT revision FROM card") != 4 || value(t, db, "SELECT next_ticket FROM board") != 2 || value(t, db, "SELECT next_event FROM board") != 5 {
		t.Fatal("counter increments")
	}
}
func TestRefusalFenceAndDuplicate(t *testing.T) {
	e, db := fixture(t)
	s := service(t, db)
	id := create(t, e, s, 0)
	i := intent(t, "card.edit", id, 0, 1, map[string]any{"title": "stale"})
	submit(t, e, s, i, id, "not_committed")
	for n := 0; n < 2; n++ {
		submit(t, e, s, i, id, "not_committed")
	}
	i = intent(t, "comment.add", id, 1, 1, map[string]any{"text": "once"})
	first := submit(t, e, s, i, id, "committed")
	second := submit(t, e, s, i, id, "committed")
	a, _ := json.Marshal(first.Terminal)
	b, _ := json.Marshal(second.Terminal)
	if string(a) != string(b) {
		t.Fatal("duplicate changed receipt")
	}
	if value(t, db, "SELECT count(*) FROM comment") != 1 || value(t, db, "SELECT revision FROM board") != 2 || value(t, db, "SELECT count(*) FROM operation") != 3 {
		t.Fatal("duplicate/refusal mutated")
	}
}
func TestCompletedAndRevoked(t *testing.T) {
	for _, cmd := range []string{"card.edit", "comment.add"} {
		t.Run(cmd, func(t *testing.T) {
			e, db := fixture(t)
			s := service(t, db)
			id := create(t, e, s, 0)
			raw(t, db, func(c *sql.Conn) error {
				_, err := c.ExecContext(testContext(t), "UPDATE card SET lane='Completed'")
				return err
			})
			input := map[string]any{"title": "deny"}
			if cmd == "comment.add" {
				input = map[string]any{"text": "deny"}
			}
			submit(t, e, s, intent(t, cmd, id, 1, 1, input), id, "not_committed")
			if value(t, db, "SELECT revision FROM board") != 1 || value(t, db, "SELECT count(*) FROM card_revision") != 0 {
				t.Fatal("completed changed")
			}
		})
	}
	e, db := fixture(t)
	s := service(t, db)
	id := create(t, e, s, 0)
	raw(t, db, func(c *sql.Conn) error {
		_, err := c.ExecContext(testContext(t), "UPDATE grant_record SET enabled=0")
		return err
	})
	r := e.Submit(testContext(t), intent(t, "card.edit", id, 1, 1, map[string]any{"title": "deny"}), s.Callbacks(Route{board, inc, id}))
	if r.Refused == nil || value(t, db, "SELECT revision FROM board") != 1 {
		t.Fatal("revocation bypass")
	}
}
func TestCounterOverflowAndRollback(t *testing.T) {
	for _, counter := range []string{"revision", "next_ticket", "next_event"} {
		t.Run(counter, func(t *testing.T) {
			e, db := fixture(t)
			s := service(t, db)
			raw(t, db, func(c *sql.Conn) error {
				_, err := c.ExecContext(testContext(t), "UPDATE board SET "+counter+"=?", contract.MaxInteger)
				return err
			})
			rev := uint64(0)
			if counter == "revision" {
				rev = contract.MaxInteger
			}
			submit(t, e, s, intent(t, "card.create", "", rev, 0, map[string]any{"title": "overflow", "description": ""}), "", "not_committed")
			if value(t, db, "SELECT count(*) FROM card") != 0 {
				t.Fatal("overflow effects")
			}
		})
	}
	e, db := fixture(t)
	s := service(t, db)
	i := intent(t, "card.create", "", 0, 0, map[string]any{"title": "rollback", "description": ""})
	cb := s.Callbacks(Route{board, inc, ""})
	apply := cb.Apply
	cb.Apply = func(ctx context.Context, w writer.Write, i contract.Intent, at A.Timestamp) (writer.Effect, error) {
		_, err := apply(ctx, w, i, at)
		if err != nil {
			return writer.Effect{}, err
		}
		return writer.Effect{}, fmt.Errorf("synthetic post-effect failure")
	}
	r := e.Submit(testContext(t), i, cb)
	if r.Unknown == nil || value(t, db, "SELECT revision FROM board") != 0 || value(t, db, "SELECT count(*) FROM card") != 0 || value(t, db, "SELECT count(*) FROM operation") != 0 {
		t.Fatal("partial failure escaped rollback")
	}
}

func TestStartPrerequisites(t *testing.T) {
	for _, mode := range []string{"approved", "unapproved", "archived", "stale-approval", "informational", "inactive"} {
		t.Run(mode, func(t *testing.T) {
			e, db := fixture(t)
			s := service(t, db)
			owner := create(t, e, s, 0)
			prereq := create(t, e, s, 1)
			raw(t, db, func(c *sql.Conn) error {
				if _, err := c.ExecContext(testContext(t), "UPDATE board SET revision=3"); err != nil {
					return err
				}
				kind := "blocking"
				active := 1
				if mode == "informational" {
					kind = "informational"
				}
				if mode == "inactive" {
					active = 0
				}
				link := "70000000-0000-0000-0000-000000000001"
				approval := "80000000-0000-0000-0000-000000000001"
				for _, q := range []struct {
					q string
					a []any
				}{
					{"INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'link',?,?,?,?)", []any{inc, link, owner, board, inc}},
					{"INSERT INTO link(incarnation,link_id,card_id,kind,target_board,target_incarnation,target_card_id,internal_target_id,active,created_at) VALUES(?,?,?,?,?,?,?,?,?,'2026-10-26T00:00:00Z')", []any{inc, link, owner, kind, board, inc, prereq, prereq, active}},
					{"INSERT INTO current_reference VALUES(?,?,'link',?)", []any{inc, owner, link}},
					{"INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'approval',?,?,?,?)", []any{inc, approval, prereq, board, inc}},
					{"INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,1,1,1,'2026-10-26T00:00:00Z')", []any{inc, approval, prereq, actor}},
					{"UPDATE card SET revision=2,lane='Completed',current_approval_id=? WHERE card_id=?", []any{approval, prereq}},
				} {
					if _, err := c.ExecContext(testContext(t), q.q, q.a...); err != nil {
						return err
					}
				}
				switch mode {
				case "unapproved":
					_, err := c.ExecContext(testContext(t), "UPDATE card SET current_approval_id=NULL WHERE card_id=?", prereq)
					return err
				case "archived":
					_, err := c.ExecContext(testContext(t), "UPDATE card SET archived=1 WHERE card_id=?", prereq)
					return err
				case "stale-approval":
					_, err := c.ExecContext(testContext(t), "UPDATE card SET revision=3 WHERE card_id=?", prereq)
					return err
				}
				return nil
			})
			want := "not_committed"
			if mode == "approved" || mode == "informational" || mode == "inactive" {
				want = "committed"
			}
			submit(t, e, s, intent(t, "card.edit", owner, 3, 1, map[string]any{"lane": "In Progress", "title": "atomic change"}), owner, want)
			if want == "not_committed" && value(t, db, "SELECT count(*) FROM card WHERE title='atomic change'") != 0 {
				t.Fatal("mixed refusal leaked title")
			}
		})
	}
}

func TestApprovalInvalidationAndAttachmentSnapshotPins(t *testing.T) {
	e, db := fixture(t)
	s := service(t, db)
	id := create(t, e, s, 0)
	approval := "80000000-0000-0000-0000-000000000001"
	attachment := "90000000-0000-0000-0000-000000000001"
	digest := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	raw(t, db, func(c *sql.Conn) error {
		for _, q := range []struct {
			q string
			a []any
		}{
			{"INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'approval',?,?,?,?)", []any{inc, approval, id, board, inc}},
			{"INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,1,1,1,'2026-10-26T00:00:00Z')", []any{inc, approval, id, actor}},
			{"UPDATE card SET current_approval_id=? WHERE card_id=?", []any{approval, id}},
			{"INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'attachment',?,?,?,?)", []any{inc, attachment, id, board, inc}},
			{"INSERT INTO blob VALUES(?,?,0,?)", []any{inc, digest, []byte{}}},
			{"INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) VALUES(?,?,?,'Synthetic','text/plain',?,0,'2026-10-26T00:00:00Z')", []any{inc, attachment, id, digest}},
			{"INSERT INTO current_reference VALUES(?,?,'attachment',?)", []any{inc, id, attachment}},
			{"INSERT INTO current_attachment_pin VALUES(?,?,?,0)", []any{inc, attachment, digest}},
		} {
			if _, err := c.ExecContext(testContext(t), q.q, q.a...); err != nil {
				return err
			}
		}
		return nil
	})
	submit(t, e, s, intent(t, "comment.add", id, 1, 1, map[string]any{"text": "invalidate"}), id, "committed")
	if value(t, db, "SELECT count(*) FROM card WHERE current_approval_id IS NULL") != 1 || value(t, db, "SELECT count(*) FROM approval") != 1 || value(t, db, "SELECT count(*) FROM revision_reference WHERE kind='approval'") != 1 || value(t, db, "SELECT count(*) FROM history_blob_pin") != 1 || value(t, db, "SELECT count(*) FROM current_attachment_pin") != 1 {
		t.Fatal("snapshot approval/pin preservation")
	}
}

func TestCompactSourceRevisionProvenance(t *testing.T) {
	e, db := fixture(t)
	s := service(t, db)
	id := create(t, e, s, 0)
	sourceBoard := "10000000-0000-0000-0000-000000000009"
	sourceInc := "20000000-0000-0000-0000-000000000009"
	raw(t, db, func(c *sql.Conn) error {
		for _, q := range []struct {
			q string
			a []any
		}{
			{"UPDATE board SET revision=9", nil},
			{"UPDATE card SET revision=2 WHERE card_id=?", []any{id}},
			{`INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision,removed_at,removal_reason,removal_revision) VALUES(?,?,?,?,0,?,?,'card.edit','card',?,4,1,'COMMITTED','OK','2026-10-25T01:02:03Z',5,2,'2026-10-25T02:00:00Z','expired',6)`, []any{inc, sourceBoard, sourceInc, op2, actor, strings.Repeat("a", 64), id}},
			{"INSERT INTO operation_reference VALUES(?,?,?,?,'card',?,2)", []any{inc, sourceBoard, sourceInc, op2, id}},
		} {
			if _, err := c.ExecContext(testContext(t), q.q, q.a...); err != nil {
				return err
			}
		}
		return nil
	})
	submit(t, e, s, intent(t, "card.edit", id, 9, 2, map[string]any{"description": "local after restore"}), id, "committed")
	if value(t, db, "SELECT count(*) FROM card_revision WHERE origin_board='"+sourceBoard+"' AND origin_incarnation='"+sourceInc+"' AND card_revision=2 AND committed_at='2026-10-25T01:02:03Z'") != 1 {
		t.Fatal("source compact provenance was rewritten")
	}
}

func TestMissingPriorProvenanceFailsClosed(t *testing.T) {
	e, db := fixture(t)
	s := service(t, db)
	id := create(t, e, s, 0)
	raw(t, db, func(c *sql.Conn) error {
		if _, err := c.ExecContext(testContext(t), "UPDATE board SET revision=2"); err != nil {
			return err
		}
		_, err := c.ExecContext(testContext(t), "UPDATE card SET revision=2")
		return err
	})
	i := intent(t, "card.edit", id, 2, 2, map[string]any{"title": "must rollback"})
	r := e.Submit(testContext(t), i, s.Callbacks(Route{board, inc, id}))
	if r.Unknown == nil || value(t, db, "SELECT revision FROM board") != 2 || value(t, db, "SELECT revision FROM card") != 2 || value(t, db, "SELECT count(*) FROM card_revision") != 0 || value(t, db, "SELECT count(*) FROM operation") != 1 {
		t.Fatal("invented missing revision provenance")
	}
}

func TestCurrentRightsAndRouteAdmission(t *testing.T) {
	e, db := fixture(t)
	s := service(t, db)
	id := create(t, e, s, 0)
	raw(t, db, func(c *sql.Conn) error {
		_, err := c.ExecContext(testContext(t), "DELETE FROM grant_right WHERE right_name='edit'")
		return err
	})
	i := intent(t, "card.edit", id, 1, 1, map[string]any{"title": "denied"})
	r := submit(t, e, s, i, id, "not_committed")
	if r.Terminal.Code != "FORBIDDEN" {
		t.Fatal("rights refusal code")
	}
	wrong := s.Callbacks(Route{board, inc, "40000000-0000-0000-0000-000000000009"})
	r = e.Submit(testContext(t), intent(t, "comment.add", id, 1, 1, map[string]any{"text": "route mismatch"}), wrong)
	if r.Terminal == nil || r.Terminal.Code != "INVALID_INPUT" || value(t, db, "SELECT revision FROM board") != 1 {
		t.Fatal("route bypass")
	}
	if New(nil).Callbacks(Route{}).Authority != nil {
		t.Fatal("missing service gained authority")
	}
}

func TestNoChangeRefusalPreservesDomainAndFence(t *testing.T) {
	for _, mode := range []string{"empty", "same-title", "same-description", "same-priority", "same-lane", "all-same"} {
		t.Run(mode, func(t *testing.T) {
			e, db := fixture(t)
			s := service(t, db)
			id := create(t, e, s, 0)
			approval := "80000000-0000-0000-0000-000000000001"
			raw(t, db, func(c *sql.Conn) error {
				for _, q := range []struct {
					q string
					a []any
				}{
					{"INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'approval',?,?,?,?)", []any{inc, approval, id, board, inc}},
					{"INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,1,1,1,'2026-10-26T00:00:00Z')", []any{inc, approval, id, actor}},
					{"UPDATE card SET current_approval_id=? WHERE card_id=?", []any{approval, id}},
				} {
					if _, err := c.ExecContext(testContext(t), q.q, q.a...); err != nil {
						return err
					}
				}
				if mode == "same-lane" || mode == "all-same" {
					_, err := c.ExecContext(testContext(t), "UPDATE card SET lane='In Progress' WHERE card_id=?", id)
					return err
				}
				return nil
			})
			input := map[string]any{}
			switch mode {
			case "same-title":
				input["title"] = " 雪🌊 "
			case "same-description":
				input["description"] = "A\r\nB\n café"
			case "same-priority":
				input["priority"] = "P3"
			case "same-lane":
				input["lane"] = "In Progress"
			case "all-same":
				input = map[string]any{"title": " 雪🌊 ", "description": "A\r\nB\n café", "priority": "P3", "lane": "In Progress"}
			}
			i := intent(t, "card.edit", id, 1, 1, input)
			r := submit(t, e, s, i, id, "not_committed")
			if r.Terminal.Code != "NO_CHANGE" {
				t.Fatal("no-change refusal code")
			}
			if value(t, db, "SELECT count(*) FROM card WHERE current_approval_id='"+approval+"' AND revision=1") != 1 || value(t, db, "SELECT count(*) FROM card_revision") != 0 || value(t, db, "SELECT revision FROM board") != 1 || value(t, db, "SELECT count(*) FROM activity") != 1 || value(t, db, "SELECT next_event FROM board") != 2 {
				t.Fatal("no-change changed approval/domain state")
			}
			// A later real edit cannot turn the earlier identity into a mutation.
			submit(t, e, s, intent(t, "card.edit", id, 1, 1, map[string]any{"title": "later"}), id, "committed")
			r = submit(t, e, s, i, id, "not_committed")
			if r.Terminal.Code != "NO_CHANGE" {
				t.Fatal("refusal fence changed")
			}
			if value(t, db, "SELECT revision FROM board") != 2 || value(t, db, "SELECT revision FROM card") != 2 || value(t, db, "SELECT count(*) FROM card_revision") != 1 || value(t, db, "SELECT next_event FROM board") != 3 || value(t, db, "SELECT count(*) FROM activity") != 2 || value(t, db, "SELECT next_ticket FROM board") != 2 {
				t.Fatal("no-change had domain effects")
			}
			if value(t, db, "SELECT count(*) FROM card_revision WHERE json_extract(state,'$.currentApprovalId')='"+approval+"'") != 1 || value(t, db, "SELECT count(*) FROM approval") != 1 {
				t.Fatal("no-change invalidated historical approval")
			}
		})
	}
}

func TestMixedRedundantLaneAndExactUnicodeChanges(t *testing.T) {
	for index, input := range []map[string]any{
		{"lane": "In Progress", "title": "Changed"},
		{"lane": "In Progress", "priority": "P2"},
		{"title": " 雪🌊 ", "description": "A\r\nB\n cafe\u0301"},
		{"priority": "P3", "title": " 雪🌊"},
	} {
		t.Run(fmt.Sprintf("case-%d", index), func(t *testing.T) {
			e, db := fixture(t)
			s := service(t, db)
			id := create(t, e, s, 0)
			raw(t, db, func(c *sql.Conn) error {
				_, err := c.ExecContext(testContext(t), "UPDATE card SET lane='In Progress' WHERE card_id=?", id)
				return err
			})
			submit(t, e, s, intent(t, "card.edit", id, 1, 1, input), id, "committed")
			if value(t, db, "SELECT revision FROM card") != 2 || value(t, db, "SELECT count(*) FROM card_revision") != 1 || value(t, db, "SELECT count(*) FROM activity") != 2 {
				t.Fatal("mixed edit increments")
			}
		})
	}
}

func TestNoChangePrecedence(t *testing.T) {
	for _, mode := range []string{"completed", "stale", "permission", "event-overflow"} {
		t.Run(mode, func(t *testing.T) {
			e, db := fixture(t)
			s := service(t, db)
			id := create(t, e, s, 0)
			raw(t, db, func(c *sql.Conn) error {
				q := "UPDATE card SET lane='Completed'"
				switch mode {
				case "stale":
					q = "UPDATE board SET revision=2"
				case "permission":
					q = "DELETE FROM grant_right WHERE right_name='edit'"
				case "event-overflow":
					q = fmt.Sprintf("UPDATE board SET next_event=%d", contract.MaxInteger)
				}
				_, err := c.ExecContext(testContext(t), q)
				return err
			})
			r := submit(t, e, s, intent(t, "card.edit", id, 1, 1, map[string]any{}), id, "not_committed")
			want := map[string]string{"completed": "COMPLETED", "stale": "REVISION_CONFLICT", "permission": "FORBIDDEN", "event-overflow": "NO_CHANGE"}[mode]
			if r.Terminal.Code != want || value(t, db, "SELECT count(*) FROM card_revision") != 0 {
				t.Fatal("no-change precedence")
			}
		})
	}
}
