//go:build windows && amd64

package recovery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/outcome"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	"golang.org/x/sys/windows"
)

const bid = "10000000-0000-0000-0000-000000000001"
const inc = "20000000-0000-0000-0000-000000000001"
const sourceInc = "20000000-0000-0000-0000-000000000002"
const human = "30000000-0000-0000-0000-000000000001"
const automation = "30000000-0000-0000-0000-000000000002"
const grant = "30000000-0000-0000-0000-000000000003"
const card = "40000000-0000-0000-0000-000000000001"
const approval = "40000000-0000-0000-0000-000000000002"
const att = "40000000-0000-0000-0000-000000000003"
const comment = "40000000-0000-0000-0000-000000000004"
const install = "60000000-0000-0000-0000-000000000001"
const webOrigin = "https://synthetic.localhost:47443"

var baseTime = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
var route = Route{bid, inc}

type fixture struct {
	store *S.Store
	svc   *Service
	clock time.Time
	mode  string
	who   string
	dir   string
}

func tc(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func testGuard(t *testing.T, r *Registry) *PinGuard {
	t.Helper()
	g, e := r.Guard(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	return g
}
func uuid(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		t.Fatal(e)
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func newFixture(t *testing.T) *fixture {
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
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	token, e := windows.OpenCurrentProcessToken()
	if e != nil {
		t.Fatal(e)
	}
	defer token.Close()
	user, e := token.GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, strings.ReplaceAll(uuid(t), "-", "")[:12])
	if e = windows.CreateDirectory(windows.StringToUTF16Ptr(dir), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}); e != nil {
		t.Fatal(e)
	}
	s, e := S.Create(tc(t), dir, schema.Identity{BoardID: bid, Incarnation: inc})
	if e != nil {
		t.Fatal(e)
	}
	f := &fixture{store: s, clock: baseTime, mode: "cli", who: human, dir: dir}
	t.Cleanup(func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	})
	f.raw(t, func(c *sql.Conn) error {
		for _, a := range []struct{ id, kind, label string }{{human, "human", "Synthetic administrator"}, {automation, "automation", "Synthetic retention maintenance"}} {
			if _, e := c.ExecContext(tc(t), "INSERT INTO actor VALUES(?,?,?,?,?,?)", inc, a.id, a.kind, a.label, bid, inc); e != nil {
				return e
			}
		}
		for _, q := range []string{"INSERT INTO grant_fact VALUES('" + inc + "','" + grant + "')", "INSERT INTO grant_record VALUES('" + inc + "','" + grant + "','" + human + "',1)"} {
			if _, e := c.ExecContext(tc(t), q); e != nil {
				return e
			}
		}
		for _, right := range []string{"history-admin", "read", "read-own-outcome"} {
			if _, e := c.ExecContext(tc(t), "INSERT INTO grant_right VALUES(?,?,?)", inc, grant, right); e != nil {
				return e
			}
		}
		return nil
	})
	g, e := grants.New(install, webOrigin, func(ctx context.Context, r writer.Read, now time.Time) (grants.Credential, error) {
		// Synthetic verifier exercises G01's live DB admission, with no rights
		// in the credential. G02 owns production cryptographic verification.
		return grants.Credential{InstallationID: install, Origin: webOrigin, BoardID: bid, Incarnation: inc, ActorID: f.who, GrantID: grant, Mode: f.mode, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	f.svc = New(s, g)
	f.svc.now = func() time.Time { return f.clock }
	return f
}
func (f *fixture) raw(t *testing.T, fn func(*sql.Conn) error) {
	t.Helper()
	l, e := f.store.Writer(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	if e = l.Do(tc(t), func(c *sql.Conn) error {
		if _, e := c.ExecContext(tc(t), "BEGIN IMMEDIATE"); e != nil {
			return e
		}
		if e := fn(c); e != nil {
			return e
		}
		return schema.Validate(tc(t), c, schema.Identity{BoardID: bid, Incarnation: inc})
	}); e != nil {
		t.Fatal(e)
	}
	if e = l.Do(tc(t), func(c *sql.Conn) error { _, e := c.ExecContext(tc(t), "COMMIT"); return e }); e != nil {
		t.Fatal(e)
	}
}
func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	f.raw(t, func(c *sql.Conn) error { _, e := c.ExecContext(tc(t), q, args...); return e })
}
func (f *fixture) value(t *testing.T, q string) int64 {
	t.Helper()
	l, e := f.store.Reader(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	var n int64
	if e = l.Do(tc(t), func(c *sql.Conn) error { return c.QueryRowContext(tc(t), q).Scan(&n) }); e != nil {
		t.Fatal(e)
	}
	return n
}
func (f *fixture) state(t *testing.T) string {
	t.Helper()
	l, e := f.store.Reader(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	var parts []string
	e = l.Do(tc(t), func(c *sql.Conn) error {
		for _, table := range []string{"board", "actor", "card", "approval", "card_revision", "operation", "operation_full", "operation_reference", "byte_fact", "entity", "comment", "attachment", "blob", "activity", "coverage_gap", "recovery_preview"} {
			rows, e := c.QueryContext(tc(t), "SELECT * FROM "+table+" ORDER BY 1,2")
			if e != nil {
				return e
			}
			cols, _ := rows.Columns()
			for rows.Next() {
				values := make([]any, len(cols))
				dest := make([]any, len(cols))
				for i := range values {
					dest[i] = &values[i]
				}
				if e = rows.Scan(dest...); e != nil {
					rows.Close()
					return e
				}
				raw, _ := json.Marshal(values)
				parts = append(parts, table+string(raw))
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return strings.Join(parts, "\n")
}
func (f *fixture) preview(t *testing.T, command string, input any) A.Preview {
	t.Helper()
	raw, _ := json.Marshal(input)
	p, e := f.svc.Preview(tc(t), route, f.who, A.PreviewRequest{Incarnation: inc, ExpectedRevision: A.Number(f.value(t, "SELECT revision FROM board")), Command: A.RecoveryCommand(command), Input: raw})
	if e != nil {
		t.Fatal(e)
	}
	if e = A.Validate(p); e != nil {
		t.Fatal(e)
	}
	return p
}
func intent(t *testing.T, p A.Preview, who, op string) contract.Intent {
	t.Helper()
	wire, _ := json.Marshal(A.Confirmation{OpID: A.UUID(op), Incarnation: p.Incarnation, Target: nil, ExpectedRevision: p.ExpectedRevision, ExpectedCardRevision: nil, Command: p.Command, Input: p.Input, PreviewHash: p.PreviewHash})
	i, e := T.FreezeMutation(wire, contract.ResolvedRoute{Command: contract.Command(p.Command), BoardID: string(p.BoardID)}, contract.AuthenticatedActor{ActorID: who})
	if e != nil {
		t.Fatal(e)
	}
	return i
}
func confirm(t *testing.T, f *fixture, p A.Preview) writer.Result {
	t.Helper()
	return f.svc.Confirm(tc(t), route, intent(t, p, f.who, uuid(t)))
}
func terminal(t *testing.T, r writer.Result, code string) {
	t.Helper()
	if r.Terminal == nil || r.Terminal.Code != code {
		t.Fatalf("terminal %s unavailable: terminal=%v unknown=%v refused=%v", code, r.Terminal != nil, r.Unknown != nil, r.Refused != nil)
	}
	if e := T.Validate(*r.Terminal); e != nil {
		t.Fatal(e)
	}
}
func maintenance(context.Context, writer.Read) (MaintenancePrincipal, error) {
	return MaintenancePrincipal{automation, "Synthetic retention maintenance"}, nil
}

func (f *fixture) seed(t *testing.T, at A.Timestamp, withBytes bool) {
	t.Helper()
	f.raw(t, func(c *sql.Conn) error {
		ctx := tc(t)
		run := func(q string, args ...any) error { _, e := c.ExecContext(ctx, q, args...); return e }
		if e := run("UPDATE board SET revision=4,next_ticket=2 WHERE singleton=1"); e != nil {
			return e
		}
		for _, v := range []struct{ kind, id string }{{"card", card}, {"approval", approval}, {"comment", comment}} {
			if e := run("INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation,creator_id,created_at) VALUES(?,?,?,?,?,?,?,?)", inc, v.kind, v.id, card, bid, inc, human, func() any {
				if v.kind == "card" {
					return nil
				}
				return string(at)
			}()); e != nil {
				return e
			}
		}
		if e := run("INSERT INTO ticket(incarnation,ticket,card_id) VALUES(?,1,?)", inc, card); e != nil {
			return e
		}
		if e := run("INSERT INTO card(incarnation,card_id,revision,title,description,priority,lane,current_approval_id) VALUES(?,?,4,'Current title','Current prose','P1','Completed',?)", inc, card, approval); e != nil {
			return e
		}
		if e := run("INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,3,3,1,?)", inc, approval, card, human, string(at)); e != nil {
			return e
		}
		if e := run("INSERT INTO comment(incarnation,comment_id,card_id,actor_id,text,created_at) VALUES(?,?,?,?,'Current immutable comment',?)", inc, comment, card, human, string(at)); e != nil {
			return e
		}
		if e := run("INSERT INTO current_reference VALUES(?,?,'comment',?)", inc, card, comment); e != nil {
			return e
		}
		state, _ := A.Canonical(A.SnapshotState{Ticket: 1, Title: "Earlier title", Description: "Earlier secret prose", Priority: "P3", Lane: "In Progress", CurrentApprovalID: nil})
		for idx, origin := range []string{inc, sourceInc} {
			if e := run("INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) VALUES(?,?,?,?,?,?,?,?)", inc, bid, origin, card, idx+1, human, string(at), string(state)); e != nil {
				return e
			}
			if e := run("INSERT INTO revision_reference VALUES(?,?,?,?,?,'approval',?)", inc, bid, origin, card, idx+1, approval); e != nil {
				return e
			}
		}
		if withBytes {
			raw := []byte{}
			h := fmt.Sprintf("%x", sha256.Sum256(raw))
			if e := run("INSERT INTO blob VALUES(?,?,0,?)", inc, h, raw); e != nil {
				return e
			}
			if e := run("INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation,created_at) VALUES(?,'attachment',?,?,?,?,?)", inc, att, card, bid, sourceInc, string(at)); e != nil {
				return e
			}
			if e := run("INSERT INTO attachment(incarnation,attachment_id,card_id,name,media_label,sha256,byte_length,created_at) VALUES(?,?,?,'Historical empty file','',?,0,?)", inc, att, card, h, string(at)); e != nil {
				return e
			}
			if e := run("INSERT INTO revision_reference VALUES(?,?,?,?,2,'attachment',?)", inc, bid, sourceInc, card, att); e != nil {
				return e
			}
			if e := run("INSERT INTO history_blob_pin VALUES(?,?,?,?,2,?,?,0)", inc, bid, sourceInc, card, att, h); e != nil {
				return e
			}
		}
		return nil
	})
}

// seedOutcome uses the real frozen intent and S02 persistence, preserving
// independently generated terminal evidence rather than inventing a hash.
func (f *fixture) seedOutcome(t *testing.T, at A.Timestamp, committed bool) contract.Intent {
	t.Helper()
	wire, _ := json.Marshal(map[string]any{"opId": uuid(t), "incarnation": inc, "target": nil, "expectedRevision": 3, "expectedCardRevision": nil, "input": map[string]any{"title": "Original title", "description": "Original private intent"}})
	i, e := T.FreezeMutation(wire, contract.ResolvedRoute{Command: "card.create", BoardID: bid}, contract.AuthenticatedActor{ActorID: human})
	if e != nil {
		t.Fatal(e)
	}
	o, e := outcome.Base(i)
	if e != nil {
		t.Fatal(e)
	}
	o.TerminalAt = at
	if committed {
		o.TerminalClass = "COMMITTED"
		o.Code = "OK"
		r, cr := A.Positive(4), A.Positive(1)
		o.CommittedRevision = &r
		o.CommittedCardRevision = &cr
		v := A.Number(1)
		o.ResultRefs = []A.FactRef{{Kind: "card", ID: card, Revision: &v}}
		o.Full = &A.FullOutcome{Intent: i.CanonicalBytes(), Result: json.RawMessage(`{"cardId":"` + card + `"}`)}
	} else {
		o.TerminalClass = "NOT_COMMITTED"
		o.Code = "FORBIDDEN"
		raw, _ := json.Marshal(deny("FORBIDDEN"))
		o.Full = &A.FullOutcome{Intent: i.CanonicalBytes(), Result: raw}
	}
	f.raw(t, func(c *sql.Conn) error { return outcome.Insert(tc(t), c, o) })
	return i
}

func TestPreviewReadOnlyOriginalTimeAndReplay(t *testing.T) {
	f := newFixture(t)
	f.seed(t, A.Timestamp(baseTime.Add(-24*time.Hour).Format(stamp)), true)
	old := f.seedOutcome(t, A.Timestamp(baseTime.Add(-24*time.Hour).Format(stamp)), true)
	before := f.state(t)
	p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	if f.state(t) != before {
		t.Fatal("preview changed persistent state")
	}
	if p.PreviewedAt != A.Timestamp(baseTime.Format(stamp)) || p.ExpiresAt != A.Timestamp(baseTime.Add(300*time.Second).Format(stamp)) || len(p.Inventory.History) != 2 || len(p.Inventory.Outcomes) != 1 || len(p.Inventory.Bytes.Reclaimable) != 1 || p.Inventory.Bytes.Reclaimable[0].ByteLength != 0 {
		t.Fatal("preview selection/time/zero-byte inventory")
	}
	f.clock = f.clock.Add(2 * time.Second)
	i := intent(t, p, human, uuid(t))
	r := f.svc.Confirm(tc(t), route, i)
	terminal(t, r, "OK")
	if f.value(t, "SELECT revision FROM board") != 5 || f.value(t, "SELECT revision FROM card") != 4 || f.value(t, "SELECT count(*) FROM card WHERE lane='Completed' AND current_approval_id='"+approval+"' AND priority='P1'") != 1 {
		t.Fatal("current Completed state changed")
	}
	if f.value(t, "SELECT count(*) FROM blob") != 0 || f.value(t, "SELECT count(*) FROM attachment") != 0 || f.value(t, "SELECT count(*) FROM entity WHERE kind='attachment' AND removed_at IS NOT NULL") != 1 || f.value(t, "SELECT count(*) FROM comment") != 1 || f.value(t, "SELECT count(*) FROM approval") != 1 {
		t.Fatal("protected current facts or historical bytes incorrect")
	}
	if f.value(t, "SELECT count(*) FROM coverage_gap") != 3 || f.value(t, "SELECT retention_version FROM board") != 1 || f.value(t, "SELECT count(*) FROM activity WHERE type='history.clear'") != 1 {
		t.Fatal("coverage/version/activity")
	}
	stable := f.state(t)
	f.clock = f.clock.Add(time.Hour)
	f.svc.previews = map[A.Digest][]byte{}
	r = f.svc.Confirm(tc(t), route, i)
	terminal(t, r, "OK")
	if f.state(t) != stable {
		t.Fatal("matching replay changed state")
	}
	base, _ := outcome.Base(old)
	lookup := f.svc.engine.Lookup(tc(t), outcome.IdentityOf(base), f.svc.grants.Authority)
	terminal(t, lookup, "OK")
	if lookup.Terminal.Retention != "compact" || lookup.Terminal.Result != nil {
		t.Fatal("compact original receipt lost")
	}
}

func TestPreviewExpiryMismatchAndUnavailabilityAreRecorded(t *testing.T) {
	for _, which := range []string{"expired", "missing", "actor", "input", "pins"} {
		t.Run(which, func(t *testing.T) {
			f := newFixture(t)
			f.seed(t, A.Timestamp(baseTime.Add(-time.Hour).Format(stamp)), false)
			p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
			want := "PREVIEW_MISMATCH"
			switch which {
			case "expired":
				f.clock = baseTime.Add(300 * time.Second)
				want = "PREVIEW_EXPIRED"
			case "missing":
				f.svc.previews = map[A.Digest][]byte{}
				want = "PREVIEW_UNAVAILABLE"
			case "actor":
				p.ActorID = automation // hash still resolves original cache; alter immutable server copy below.
				original, _ := f.svc.cached(p.PreviewHash)
				original.ActorID = automation
				raw, _ := json.Marshal(original)
				f.svc.previews[p.PreviewHash] = raw
			case "input":
				p.Input = json.RawMessage(`{"throughTime":"2026-10-25T12:00:00Z"}`)
			case "pins":
				g := testGuard(t, f.svc.registry)
				fake := &ExportSnapshot{}
				f.svc.registry.leases[fake] = []A.ByteFact{{SHA256: A.Digest(strings.Repeat("1", 64)), ByteLength: 0}}
				g.Close()
				defer func() { g := testGuard(t, f.svc.registry); delete(f.svc.registry.leases, fake); g.Close() }()
			}
			r := confirm(t, f, p)
			if which == "actor" {
				want = "PREVIEW_UNAVAILABLE"
			}
			terminal(t, r, want)
			if r.Terminal.Outcome != "not_committed" || f.value(t, "SELECT revision FROM board") != 4 || f.value(t, "SELECT count(*) FROM card_revision WHERE state IS NOT NULL") != 2 || f.value(t, "SELECT count(*) FROM operation") != 1 {
				t.Fatal("refusal had effects or lacked durable terminal")
			}
		})
	}
}

func TestCurrentRightsAndRevisionRechecked(t *testing.T) {
	for _, which := range []string{"read", "history-admin", "revoked", "revision", "native", "automation"} {
		t.Run(which, func(t *testing.T) {
			f := newFixture(t)
			p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
			switch which {
			case "read", "history-admin":
				f.exec(t, "DELETE FROM grant_right WHERE right_name=?", which)
			case "revoked":
				f.exec(t, "UPDATE grant_record SET enabled=0")
			case "revision":
				f.exec(t, "UPDATE board SET revision=1")
			case "native":
				f.mode = "native-owner"
			case "automation":
				f.exec(t, "UPDATE grant_record SET actor_id=?", automation)
				f.who = automation
			}
			r := confirm(t, f, p)
			if which == "revoked" || which == "native" || which == "automation" {
				if r.Refused == nil || r.Refused.Error.Code != "FORBIDDEN" {
					t.Fatal("current credential/kind admission bypass")
				}
			} else {
				code := "FORBIDDEN"
				if which == "revision" {
					code = "REVISION_CONFLICT"
				}
				terminal(t, r, code)
			}
			if f.value(t, "SELECT count(*) FROM activity") != 0 {
				t.Fatal("refused request generated activity")
			}
		})
	}
}

func TestRetentionBoundaryFloorsAndMaximumVersion(t *testing.T) {
	f := newFixture(t)
	cut := baseTime.Add(-24 * time.Hour)
	f.seed(t, A.Timestamp(cut.Format(stamp)), true)
	f.seedOutcome(t, A.Timestamp(cut.Add(time.Second).Format(stamp)), false)
	f.exec(t, "INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,actor_id,op_id,type,board_revision,at,affected) VALUES(?,?,?,?,?,?,'card.create',4,?,'[]')", inc, bid, sourceInc, 1, human, uuid(t), cut.Format(stamp))
	p := f.preview(t, "retention.set", A.Retention{HistoryDays: 1, ActivityDays: 1, OutcomeDays: 1})
	if p.Inventory.Counts.HistorySnapshots != 2 || p.Inventory.Counts.ActivityEvents != 1 || p.Inventory.Counts.OutcomePayloads != 0 {
		t.Fatal("inclusive boundary")
	}
	terminal(t, confirm(t, f, p), "OK")
	if f.value(t, "SELECT retention_version FROM board") != 2 || f.value(t, "SELECT count(*) FROM activity WHERE type='retention.set'") != 1 || f.value(t, "SELECT count(*) FROM coverage_gap WHERE reason='expired'") != 3 {
		t.Fatal("policy prune transaction")
	}
	f.exec(t, "UPDATE board SET retention_version=9007199254740991")
	f.clock = f.clock.Add(time.Second)
	r, e := f.svc.Expire(tc(t), route, maintenance)
	if e != nil || r.Unknown || r.Revision == nil {
		t.Fatal("expiry must work at max retention version", e)
	}
	if f.value(t, "SELECT retention_version FROM board") != 9007199254740991 || f.value(t, "SELECT count(*) FROM operation_full") != 1 {
		t.Fatal("maintenance changed policy or failed outcome boundary")
	}
}

func TestMaintenanceAuthorityAndAtomicity(t *testing.T) {
	for _, which := range []string{"absent", "human", "label", "wrong-board", "rollback", "after-commit"} {
		t.Run(which, func(t *testing.T) {
			f := newFixture(t)
			f.seed(t, A.Timestamp(baseTime.Add(-400*24*time.Hour).Format(stamp)), true)
			before := f.state(t)
			verify := MaintenanceVerifier(maintenance)
			rt := route
			switch which {
			case "absent":
				verify = nil
			case "human":
				verify = func(context.Context, writer.Read) (MaintenancePrincipal, error) {
					return MaintenancePrincipal{human, "Synthetic administrator"}, nil
				}
			case "label":
				verify = func(context.Context, writer.Read) (MaintenancePrincipal, error) {
					return MaintenancePrincipal{automation, "Forged label"}, nil
				}
			case "wrong-board":
				rt.BoardID = human
			case "rollback":
				f.svc.fault = func(stage string) error {
					if stage == "maintenance-before-commit" {
						return errors.New("injected")
					}
					return nil
				}
			case "after-commit":
				f.svc.fault = func(stage string) error {
					if stage == "maintenance-after-commit" {
						return errors.New("injected")
					}
					return nil
				}
			}
			r, e := f.svc.Expire(tc(t), rt, verify)
			if e == nil {
				t.Fatal("maintenance failure expected")
			}
			if which == "after-commit" {
				if !r.Unknown || f.value(t, "SELECT revision FROM board") != 5 || f.value(t, "SELECT count(*) FROM activity WHERE type='recovery.expire' AND op_id IS NULL AND actor_id='"+automation+"'") != 1 {
					t.Fatal("lost commit evidence")
				}
				if _, e := f.store.Writer(tc(t)); !errors.Is(e, S.ErrFenced) {
					t.Fatal("uncertain maintenance not fenced")
				}
			} else if f.state(t) != before {
				t.Fatal("failed maintenance changed state")
			}
		})
	}
}

func TestApplyFailureRollsBackWithoutFabricatedNoncommit(t *testing.T) {
	f := newFixture(t)
	f.seed(t, A.Timestamp(baseTime.Add(-time.Hour).Format(stamp)), true)
	p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	before := f.state(t)
	f.svc.fault = func(stage string) error { return errors.New("injected application failure") }
	r := confirm(t, f, p)
	if r.Unknown == nil || r.Terminal != nil || f.state(t) != before {
		t.Fatal("failed transaction fabricated a terminal or left effects")
	}
}

func TestPreviewInputBoundsAndSecretExclusion(t *testing.T) {
	f := newFixture(t)
	for _, input := range []string{`{"historyDays":0,"activityDays":1,"outcomeDays":1}`, `{"historyDays":3651,"activityDays":1,"outcomeDays":1}`, `{"historyDays":1,"activityDays":1}`, `{"historyDays":1,"activityDays":1,"outcomeDays":1,"extra":"secret"}`, `{"historyDays":9007199254740992,"activityDays":1,"outcomeDays":1}`} {
		if _, e := f.svc.Preview(tc(t), route, human, A.PreviewRequest{Incarnation: inc, Command: "retention.set", Input: json.RawMessage(input)}); e == nil {
			t.Fatal("invalid policy accepted")
		}
	}
	if _, e := f.svc.Preview(tc(t), route, human, A.PreviewRequest{Incarnation: inc, Command: "history.clear", Input: json.RawMessage(`{"throughTime":"2026-10-26T12:00:01Z"}`)}); e == nil {
		t.Fatal("future clear accepted")
	}
	p := f.preview(t, "retention.set", A.Retention{HistoryDays: 3650, ActivityDays: 3650, OutcomeDays: 3650})
	raw, _ := A.Canonical(p)
	for _, text := range []string{"credential", "bearer", "rights", "path", "Synthetic administrator", "Earlier secret prose"} {
		if strings.Contains(string(raw), text) {
			t.Fatal("sensitive/noninventory preview material")
		}
	}
	if len(p.PreviewHash) != 64 {
		t.Fatal("hash shape")
	}
	_, e := hex.DecodeString(string(p.PreviewHash))
	if e != nil {
		t.Fatal(e)
	}
}

func TestExportSnapshotGuardAndReleaseCleanup(t *testing.T) {
	f := newFixture(t)
	f.seed(t, A.Timestamp(baseTime.Add(-time.Hour).Format(stamp)), true)
	if RegistryFor(f.store) != f.svc.registry {
		t.Fatal("split pin registry")
	}
	x, e := f.svc.registry.BeginExportSnapshot(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { x.Close() })
	id, rev := x.Identity()
	if id != (A.Identity{BoardID: bid, Incarnation: inc}) || rev != 4 || len(x.Facts()) != 1 {
		t.Fatal("snapshot anchor")
	}
	p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	if len(p.Inventory.Bytes.ExportPinned) != 1 || len(p.Inventory.Bytes.Reclaimable) != 0 {
		t.Fatal("export union missing")
	}
	// A recovery guard may be held while SQLite COMMIT waits on this reader.
	// Close must free the reader before waiting on that guard/writer slot.
	guardHeld := make(chan struct{})
	closeStarted := make(chan struct{})
	confirmDone := make(chan writer.Result, 1)
	i := intent(t, p, human, uuid(t))
	f.svc.fault = func(stage string) error {
		if stage == "after-prune" {
			close(guardHeld)
			<-closeStarted
		}
		return nil
	}
	go func() { confirmDone <- f.svc.Confirm(context.Background(), route, i) }()
	<-guardHeld
	closeDone := make(chan error, 1)
	go func() { close(closeStarted); closeDone <- x.Close() }()
	select {
	case r := <-confirmDone:
		terminal(t, r, "OK")
	case <-time.After(5 * time.Second):
		t.Fatal("reader/registry COMMIT deadlock")
	}
	select {
	case e = <-closeDone:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("export close deadlock")
	}
	if f.value(t, "SELECT count(*) FROM blob") != 0 || f.value(t, "SELECT revision FROM board") != 5 {
		t.Fatal("last-pin cleanup or revision")
	}
	if e = x.Read(tc(t), func(writer.Read) error { return nil }); e == nil {
		t.Fatal("closed snapshot readable")
	}
}

func TestDynamicExportPinsInvalidateExactPreview(t *testing.T) {
	f := newFixture(t)
	f.seed(t, A.Timestamp(baseTime.Add(-time.Hour).Format(stamp)), true)
	p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	x, e := f.svc.registry.BeginExportSnapshot(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { x.Close() })
	// Model Close's first step: its actual reader has closed, while its pin
	// registration still awaits serialized writer admission. The subsequent
	// Confirm can durably record the changed-inventory refusal.
	if e = x.reader.Close(); e != nil {
		t.Fatal(e)
	}
	terminal(t, confirm(t, f, p), "PREVIEW_MISMATCH")
	if e = x.Close(); e != nil {
		t.Fatal(e)
	}
	p = f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	terminal(t, confirm(t, f, p), "OK")
}

func TestUnreleasedReaderCommitUnknownNoRetry(t *testing.T) {
	f := newFixture(t)
	f.seed(t, A.Timestamp(baseTime.Add(-time.Hour).Format(stamp)), true)
	p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	x, e := f.svc.registry.BeginExportSnapshot(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { x.Close() })
	p = f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	before := f.state(t)
	r := confirm(t, f, p)
	if r.Unknown == nil || r.Terminal != nil || f.state(t) != before {
		t.Fatal("blocked COMMIT inferred receipt/retried mutation")
	}
	if e = x.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestDistinctQuotaAndNonpinningFacts(t *testing.T) {
	h := A.Digest(strings.Repeat("a", 64))
	m := map[A.Digest]A.Number{}
	if e := addFact(m, A.ByteFact{SHA256: h, ByteLength: 0}); e != nil {
		t.Fatal(e)
	}
	if e := addFact(m, A.ByteFact{SHA256: h, ByteLength: 1}); e == nil {
		t.Fatal("length disagreement accepted")
	}
	facts := make([]A.ByteFact, 7)
	for i := range facts {
		facts[i] = A.ByteFact{SHA256: A.Digest(fmt.Sprintf("%064x", i+1)), ByteLength: 10485760}
	}
	facts[6].ByteLength = 4194304
	if n, e := factSum(facts); e != nil || n != 67108864 {
		t.Fatal("inclusive 64MiB cap")
	}
	facts[6].ByteLength++
	if _, e := factSum(facts); e == nil {
		t.Fatal("cap overflow accepted")
	}
	f := newFixture(t)
	f.seed(t, A.Timestamp(baseTime.Add(-time.Hour).Format(stamp)), true)
	p := f.preview(t, "history.clear", A.ClearInput{ThroughTime: A.Timestamp(baseTime.Format(stamp))})
	terminal(t, confirm(t, f, p), "OK")
	g := testGuard(t, f.svc.registry)
	defer g.Close()
	l, e := f.store.Reader(tc(t))
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	e = l.Do(tc(t), func(c *sql.Conn) error {
		m, e := domainPins(readAccess{tc(t), c}, nil)
		if e != nil {
			return e
		}
		if len(m) != 0 {
			return errors.New("compact facts pinned bytes")
		}
		return g.CheckQuota(readAccess{tc(t), c})
	})
	if e != nil {
		t.Fatal(e)
	}
}

func TestTimestampAndCounterLimits(t *testing.T) {
	if _, e := floor(time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), 3650); e == nil {
		t.Fatal("negative timestamp overflow")
	}
	f := newFixture(t)
	f.exec(t, "UPDATE board SET revision=9007199254740991")
	if _, e := f.svc.Preview(tc(t), route, human, A.PreviewRequest{Incarnation: inc, ExpectedRevision: 9007199254740991, Command: "history.clear", Input: json.RawMessage(`{"throughTime":"2026-10-26T12:00:00Z"}`)}); e == nil {
		t.Fatal("max counter overflow")
	}
	if _, e := previewTime(func() time.Time { return time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC) }); e == nil {
		t.Fatal("preview expiry overflow")
	}
}

func TestFloorsNeverRegressAndClearDoesNotSetFloors(t *testing.T) {
	f := newFixture(t)
	f.exec(t, "UPDATE board SET history_floor='2026-10-26T11:00:00Z',activity_floor='2026-10-26T11:00:00Z',outcome_floor='2026-10-26T11:00:00Z'")
	p := f.preview(t, "retention.set", A.Retention{HistoryDays: 3650, ActivityDays: 3650, OutcomeDays: 3650})
	if p.Inventory.ProposedFloors.HistoryFloor == nil || *p.Inventory.ProposedFloors.HistoryFloor != "2026-10-26T11:00:00Z" {
		t.Fatal("floor regressed")
	}
	terminal(t, confirm(t, f, p), "OK")
	p = f.preview(t, "history.clear", A.ClearInput{ThroughTime: "2026-10-26T12:00:00Z"})
	floors := p.Inventory.ProposedFloors
	terminal(t, confirm(t, f, p), "OK")
	next := f.preview(t, "history.clear", A.ClearInput{ThroughTime: "2026-10-26T12:00:00Z"})
	if !reflect.DeepEqual(floors, next.Inventory.ProposedFloors) || len(next.Inventory.CoverageGaps) != 0 {
		t.Fatal("empty clear invented coverage/floors")
	}
}
