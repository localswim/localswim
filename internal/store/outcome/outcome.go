// Package outcome persists current-authority terminal facts. These low-level
// helpers establish no authentication; only writer may expose their results after
// checking independently authenticated current authority in the same snapshot.
package outcome

import (
	"context"
	"database/sql"
	"errors"
	"reflect"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/schema"
)

var ErrMismatch = errors.New("operation identity mismatch")

// LookupIdentity is the exact D03 lookup evidence: route, query and independently
// authenticated actor. No command, payload, target or precondition is supplied.
// It is an internal argument, not a new wire or retained-file DTO.
type LookupIdentity struct {
	BoardID            string
	Incarnation        string
	OpID               string
	ActorID            string
	FingerprintVersion uint64
	Fingerprint        string
}

func (k LookupIdentity) Validate() error {
	if !schema.ValidUUID(k.BoardID) || !schema.ValidUUID(k.Incarnation) || !schema.ValidUUID(k.OpID) || !schema.ValidUUID(k.ActorID) || k.FingerprintVersion != 1 || len(k.Fingerprint) != 64 {
		return errors.New("invalid lookup identity")
	}
	for _, ch := range k.Fingerprint {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return errors.New("invalid lookup fingerprint")
		}
	}
	return nil
}

func IdentityOf(o A.Operation) LookupIdentity {
	return LookupIdentity{BoardID: string(o.Origin.BoardID), Incarnation: string(o.Origin.Incarnation), OpID: string(o.OpID), ActorID: string(o.ActorID), FingerprintVersion: uint64(o.FingerprintVersion), Fingerprint: string(o.Fingerprint)}
}

// Evidence creates only the known identity for an unknown response; it never
// invents a semantic Intent, command, target, precondition or terminal fact.
func (k LookupIdentity) Evidence() A.Operation {
	return A.Operation{Origin: A.Identity{BoardID: A.UUID(k.BoardID), Incarnation: A.UUID(k.Incarnation)}, OpID: A.UUID(k.OpID), ActorID: A.UUID(k.ActorID), FingerprintVersion: A.Version(k.FingerprintVersion), Fingerprint: A.Digest(k.Fingerprint)}
}

// Base requires a nonzero C01 frozen intent and the D03 precondition refinement.
// It does not accept an actor or a replacement identity separately from Intent.
func Base(i contract.Intent) (A.Operation, error) {
	id, op, actor, target, pre, err := i.Context()
	if err != nil || len(i.CanonicalBytes()) == 0 || i.Fingerprint() == "" {
		return A.Operation{}, errors.New("frozen intent required")
	}
	o := A.Operation{Origin: A.Identity{BoardID: A.UUID(id.BoardID), Incarnation: A.UUID(id.Incarnation)}, OpID: A.UUID(op), ActorID: A.UUID(actor), FingerprintVersion: 1, Fingerprint: A.Digest(i.Fingerprint()), Command: A.Command(i.Command()), ExpectedRevision: A.Number(pre.ExpectedRevision), ResultRefs: []A.FactRef{}, ByteFacts: []A.ByteFact{}}
	if target != nil {
		o.Target = &A.Target{}
		if target.CardID != "" {
			v := A.UUID(target.CardID)
			o.Target.CardID = &v
		}
		if target.GrantID != "" {
			v := A.UUID(target.GrantID)
			o.Target.GrantID = &v
		}
	}
	if pre.ExpectedCardRevision != nil {
		v := A.Positive(*pre.ExpectedCardRevision)
		o.ExpectedCardRevision = &v
	}
	// C01 foundation permits card revision zero; exact D03 does not.
	if o.ExpectedCardRevision != nil && *o.ExpectedCardRevision == 0 {
		return A.Operation{}, errors.New("positive card precondition required")
	}
	for _, d := range contract.Commands() {
		if d.Command == i.Command() && (d.Target == "card") != (o.ExpectedCardRevision != nil) {
			return A.Operation{}, errors.New("exact card precondition required")
		}
	}
	if in, e := i.Input(); e == nil {
		if v, ok := in.(*contract.AttachmentInput); ok {
			o.ByteFacts = append(o.ByteFacts, A.ByteFact{SHA256: A.Digest(v.SHA256), ByteLength: A.Number(v.ByteLength)})
		}
	} else {
		return A.Operation{}, e
	}
	return o, nil
}

// Match compares all retained immutable fields, even for compact evidence.
func Match(o, original A.Operation) error {
	a, b := o, original
	a.TerminalClass, a.Code, a.TerminalAt, a.CommittedRevision, a.CommittedCardRevision, a.ResultRefs, a.Full, a.Removal = "", "", "", nil, nil, nil, nil, nil
	b.TerminalClass, b.Code, b.TerminalAt, b.CommittedRevision, b.CommittedCardRevision, b.ResultRefs, b.Full, b.Removal = "", "", "", nil, nil, nil, nil, nil
	if !reflect.DeepEqual(a, b) {
		return ErrMismatch
	}
	return nil
}

func target(o A.Operation) (any, any) {
	if o.Target == nil {
		return nil, nil
	}
	if o.Target.CardID != nil {
		return "card", string(*o.Target.CardID)
	}
	return "grant", string(*o.Target.GrantID)
}
func positive(v *A.Positive) any {
	if v == nil {
		return nil
	}
	return uint64(*v)
}

// Insert is called only inside the shared writer transaction, after domain
// staging. There is no overwrite, upsert, compaction or retry path here.
func Insert(ctx context.Context, c *sql.Conn, o A.Operation) error {
	if o.Full == nil || o.Removal != nil {
		return errors.New("new full terminal required")
	}
	if err := A.Validate(o); err != nil {
		return errors.New("invalid terminal facts")
	}
	if _, err := Terminal(o); err != nil {
		return err
	}
	k, id := target(o)
	args := []any{string(o.Origin.Incarnation), string(o.Origin.BoardID), string(o.Origin.Incarnation), string(o.OpID), string(o.ActorID), string(o.Fingerprint), string(o.Command), k, id, uint64(o.ExpectedRevision), positive(o.ExpectedCardRevision), string(o.TerminalClass), o.Code, string(o.TerminalAt), positive(o.CommittedRevision), positive(o.CommittedCardRevision)}
	_, err := c.ExecContext(ctx, `INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,fingerprint_version,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision) VALUES(?,?,?,?,1,?,?,1,?,?,?,?,?,?,?,?,?,?)`, args...)
	if err != nil {
		return err
	}
	key := args[:4]
	_, err = c.ExecContext(ctx, `INSERT INTO operation_full(incarnation,origin_board,origin_incarnation,op_id,intent,result) VALUES(?,?,?,?,?,?)`, append(append([]any{}, key...), string(o.Full.Intent), string(o.Full.Result))...)
	if err != nil {
		return err
	}
	for _, r := range o.ResultRefs {
		var rev any
		if r.Revision != nil {
			rev = uint64(*r.Revision)
		}
		_, err = c.ExecContext(ctx, `INSERT INTO operation_reference(incarnation,origin_board,origin_incarnation,op_id,kind,entity_id,revision) VALUES(?,?,?,?,?,?,?)`, append(append([]any{}, key...), string(r.Kind), string(r.ID), rev)...)
		if err != nil {
			return err
		}
	}
	for _, f := range o.ByteFacts {
		_, err = c.ExecContext(ctx, `INSERT INTO byte_fact(incarnation,origin_board,origin_incarnation,op_id,sha256,byte_length) VALUES(?,?,?,?,?,?)`, append(append([]any{}, key...), string(f.SHA256), uint64(f.ByteLength))...)
		if err != nil {
			return err
		}
	}
	if o.Command == "attachment.add" && o.TerminalClass == "COMMITTED" {
		var attachment string
		for _, r := range o.ResultRefs {
			if r.Kind == "attachment" {
				attachment = string(r.ID)
			}
		}
		f := o.ByteFacts[0]
		_, err = c.ExecContext(ctx, `INSERT INTO operation_blob_pin(incarnation,origin_board,origin_incarnation,op_id,attachment_id,sha256,byte_length) VALUES(?,?,?,?,?,?,?)`, append(append([]any{}, key...), attachment, string(f.SHA256), uint64(f.ByteLength))...)
	}
	return err
}

// Load never consults archived source rows, even when the opId is identical.
// Missing/corrupt evidence is an error, never a NOT_COMMITTED receipt.
func Load(ctx context.Context, c *sql.Conn, original A.Operation) (A.Operation, error) {
	o, err := Lookup(ctx, c, IdentityOf(original))
	if err != nil {
		return o, err
	}
	if err = Match(o, original); err != nil {
		return o, err
	}
	return o, nil
}

// Lookup resolves current retained facts from only the public lookup identity.
// Command/target/preconditions are read from the immutable row, never fabricated
// from pruned payloads. A caller must authorize before exposing this result.
func Lookup(ctx context.Context, c *sql.Conn, requested LookupIdentity) (A.Operation, error) {
	var o A.Operation
	if err := requested.Validate(); err != nil {
		return o, err
	}
	var tk, ti, ra, rr sql.NullString
	var ec, cr, cc, rv sql.NullInt64
	var version int64
	var board, inc, op, actor, fp, command, class, code, at string
	k := []any{requested.Incarnation, requested.BoardID, requested.Incarnation, requested.OpID}
	err := c.QueryRowContext(ctx, `SELECT origin_board,origin_incarnation,op_id,actor_id,fingerprint_version,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision,removed_at,removal_reason,removal_revision FROM operation WHERE incarnation=? AND origin_board=? AND origin_incarnation=? AND op_id=? AND current_ledger=1`, k...).Scan(&board, &inc, &op, &actor, &version, &fp, &command, &tk, &ti, &o.ExpectedRevision, &ec, &class, &code, &at, &cr, &cc, &ra, &rr, &rv)
	if err != nil {
		return o, err
	}
	o.Origin = A.Identity{BoardID: A.UUID(board), Incarnation: A.UUID(inc)}
	o.OpID = A.UUID(op)
	o.ActorID = A.UUID(actor)
	o.FingerprintVersion = A.Version(version)
	o.Fingerprint = A.Digest(fp)
	o.Command = A.Command(command)
	o.TerminalClass = A.TerminalClass(class)
	o.Code = code
	o.TerminalAt = A.Timestamp(at)
	if tk.Valid {
		o.Target = &A.Target{}
		v := A.UUID(ti.String)
		if tk.String == "card" {
			o.Target.CardID = &v
		} else if tk.String == "grant" {
			o.Target.GrantID = &v
		} else {
			return o, errors.New("invalid target")
		}
	}
	if ec.Valid {
		v := A.Positive(ec.Int64)
		o.ExpectedCardRevision = &v
	}
	if cr.Valid {
		v := A.Positive(cr.Int64)
		o.CommittedRevision = &v
	}
	if cc.Valid {
		v := A.Positive(cc.Int64)
		o.CommittedCardRevision = &v
	}
	if ra.Valid {
		o.Removal = &A.Removal{Reason: A.Reason(rr.String), RemovedAt: A.Timestamp(ra.String), BoardRevision: A.Number(rv.Int64)}
	}
	var intent, result string
	err = c.QueryRowContext(ctx, `SELECT intent,result FROM operation_full WHERE incarnation=? AND origin_board=? AND origin_incarnation=? AND op_id=?`, k...).Scan(&intent, &result)
	if err == nil {
		o.Full = &A.FullOutcome{Intent: []byte(intent), Result: []byte(result)}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return o, err
	}
	o.ResultRefs = []A.FactRef{}
	o.ByteFacts = []A.ByteFact{}
	rows, err := c.QueryContext(ctx, `SELECT kind,entity_id,revision FROM operation_reference WHERE incarnation=? AND origin_board=? AND origin_incarnation=? AND op_id=? ORDER BY kind,entity_id`, k...)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var kind, id string
		var rev sql.NullInt64
		if err = rows.Scan(&kind, &id, &rev); err != nil {
			break
		}
		r := A.FactRef{Kind: A.FactKind(kind), ID: A.UUID(id)}
		if rev.Valid {
			v := A.Number(rev.Int64)
			r.Revision = &v
		}
		o.ResultRefs = append(o.ResultRefs, r)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return o, err
	}
	rows, err = c.QueryContext(ctx, `SELECT sha256,byte_length FROM byte_fact WHERE incarnation=? AND origin_board=? AND origin_incarnation=? AND op_id=? ORDER BY sha256`, k...)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var f A.ByteFact
		if err = rows.Scan(&f.SHA256, &f.ByteLength); err != nil {
			break
		}
		o.ByteFacts = append(o.ByteFacts, f)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return o, err
	}
	if err = A.Validate(o); err != nil {
		return o, errors.New("invalid retained evidence")
	}
	if IdentityOf(o) != requested {
		return o, ErrMismatch
	}
	return o, nil
}

func Terminal(o A.Operation) (T.Terminal, error) {
	t := T.Terminal{APIVersion: 1, BoardID: o.Origin.BoardID, Incarnation: o.Origin.Incarnation, OpID: o.OpID, ActorID: o.ActorID, FingerprintVersion: o.FingerprintVersion, Fingerprint: o.Fingerprint, Command: o.Command, Target: o.Target, ExpectedRevision: o.ExpectedRevision, ExpectedCardRevision: o.ExpectedCardRevision, TerminalAt: o.TerminalAt, Code: o.Code, Revision: o.CommittedRevision, CardRevision: o.CommittedCardRevision, Retention: "compact", ResultRefs: o.ResultRefs, ByteFacts: o.ByteFacts, Removal: o.Removal}
	if o.TerminalClass == "COMMITTED" {
		t.Outcome = "committed"
	} else if o.TerminalClass == "NOT_COMMITTED" {
		t.Outcome = "not_committed"
	} else {
		return t, errors.New("invalid terminal class")
	}
	if o.Full != nil {
		t.Retention = "full"
		r := o.Full.Result
		t.Result = &r
	}
	if err := T.Validate(t); err != nil {
		return T.Terminal{}, errors.New("invalid terminal envelope")
	}
	return t, nil
}
