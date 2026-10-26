package transport

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
)

var enums = map[string][]string{
	"TerminalOutcome": {"committed", "not_committed"}, "RetentionClass": {"full", "compact"}, "RefusedOutcome": {"refused"}, "UnknownOutcome": {"unknown"},
	"BoardScope": {"board"}, "CardScope": {"card"}, "HistoryScope": {"history"}, "SessionScope": {"session"}, "RevokedOutcome": {"revoked"},
	"CredentialScope": {"credential"}, "CredentialCommand": {"issue", "revoke"}, "CredentialOutcome": {"confirmed", "refused", "failed", "unavailable", "unknown"},
	"ExportScope": {"export"}, "ExportOutcome": {"confirmed", "offered", "failed", "unknown"},
}
var rights = []string{"approve", "attach", "comment", "create", "edit", "export", "grant-admin", "history-admin", "read", "read-own-outcome", "restore", "review-handoff"}
var v4RE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var fieldRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// Decode requires complete D03 fields and delegates shared scalars, exact rows,
// ordering and local invariants to C03. Errors never echo received text/secrets.
// It proves boundary validity only, never that a terminal claim is authentic.
func Decode[T any](data []byte) (T, error) {
	var out T
	if !supported(reflect.TypeFor[T]()) {
		return out, fmt.Errorf("unsupported transport boundary")
	}
	if reflect.TypeFor[T]() == reflect.TypeFor[NonterminalRefusal]() {
		x, e := decodeRefusal(data)
		if e != nil {
			return out, e
		}
		out = any(x).(T)
	} else {
		x, e := A.Decode[T](data)
		if e != nil {
			return out, fmt.Errorf("invalid transport structure")
		}
		out = x
	}
	if e := walk(reflect.ValueOf(out)); e != nil {
		return *new(T), e
	}
	return out, nil
}

func decodeRefusal(data []byte) (NonterminalRefusal, error) {
	var out NonterminalRefusal
	v, e := contract.DecodeJSON(data)
	if e != nil {
		return out, fmt.Errorf("invalid transport JSON")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return out, fmt.Errorf("invalid refusal object")
	}
	if op, ok := m["opId"]; ok {
		raw, _ := contract.Canonical(op)
		id, e := A.Decode[A.UUID](raw)
		if e != nil {
			return out, fmt.Errorf("invalid optional opId")
		}
		out.OpID = &id
		delete(m, "opId")
	}
	b, e := contract.Canonical(m)
	if e != nil {
		return out, fmt.Errorf("invalid refusal")
	}
	base, e := A.Decode[struct {
		APIVersion A.Version      `json:"apiVersion"`
		Outcome    RefusedOutcome `json:"outcome"`
		Error      Error          `json:"error"`
		Conflict   *Conflict      `json:"conflict"`
	}](b)
	if e != nil {
		return out, fmt.Errorf("invalid refusal structure")
	}
	out.APIVersion = base.APIVersion
	out.Outcome = base.Outcome
	out.Error = base.Error
	out.Conflict = base.Conflict
	return out, nil
}

// Validate uses the same boundary for in-memory construction. Unknown optional
// facts remain null; nil required arrays never become empty collections.
func Validate(v any) error {
	if v == nil {
		return fmt.Errorf("nil transport boundary")
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return fmt.Errorf("nil transport boundary")
		}
		return Validate(rv.Elem().Interface())
	}
	if !supported(rv.Type()) {
		return fmt.Errorf("unsupported transport boundary")
	}
	if e := stringsValid(rv); e != nil {
		return e
	}
	p := plain(v)
	if x, ok := v.(NonterminalRefusal); ok {
		b, e := json.Marshal(x)
		if e != nil {
			return fmt.Errorf("invalid refusal")
		}
		_, e = Decode[NonterminalRefusal](b)
		return e
	}
	if e := A.Validate(p); e != nil {
		return fmt.Errorf("invalid transport structure")
	}
	return walk(reflect.ValueOf(v))
}

func supported(t reflect.Type) bool {
	if t == reflect.TypeFor[Terminal]() || t == reflect.TypeFor[PreviewRequest]() || t == reflect.TypeFor[Preview]() || t == reflect.TypeFor[Confirmation]() || t == reflect.TypeFor[RestoreStatus]() {
		return true
	}
	for _, v := range models {
		if t == reflect.TypeOf(v) {
			return true
		}
	}
	return false
}
func stringsValid(v reflect.Value) error {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		return stringsValid(v.Elem())
	}
	if v.Type() == reflect.TypeFor[json.RawMessage]() {
		if _, e := contract.DecodeJSON(v.Bytes()); e != nil {
			return fmt.Errorf("invalid result bytes")
		}
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return fmt.Errorf("invalid Unicode string")
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if e := stringsValid(v.Field(i)); e != nil {
				return e
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if e := stringsValid(v.Index(i)); e != nil {
				return e
			}
		}
	}
	return nil
}

func walk(v reflect.Value) error {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		return walk(v.Elem())
	}
	if values := enums[v.Type().Name()]; len(values) > 0 {
		found := false
		for _, x := range values {
			found = found || v.String() == x
		}
		if !found {
			return fmt.Errorf("invalid transport discriminator")
		}
	}
	switch x := v.Interface().(type) {
	case UUIDv4:
		if !v4RE.MatchString(string(x)) {
			return fmt.Errorf("invalid UUIDv4")
		}
	case SecretCode:
		if !canonicalSecret(string(x), 16) {
			return fmt.Errorf("invalid code encoding")
		}
	case SecretToken:
		if !canonicalSecret(string(x), 32) {
			return fmt.Errorf("invalid token encoding")
		}
	case Rights:
		prior := ""
		for i, r := range x {
			found := false
			for _, q := range rights {
				found = found || r == q
			}
			if !found || (i > 0 && r <= prior) {
				return fmt.Errorf("invalid sorted unique rights")
			}
			prior = r
		}
	case Error:
		if x.Code == "" || x.Message == "" || (x.Field != nil && !fieldRE.MatchString(*x.Field)) {
			return fmt.Errorf("invalid public diagnostic")
		}
	case CredentialError:
		if x.Code == "" || x.Message == "" {
			return fmt.Errorf("invalid credential diagnostic")
		}
	case Conflict:
		if (x.CardID == nil) != (x.CardRevision == nil) {
			return fmt.Errorf("conflict pair")
		}
	case Terminal:
		if e := terminal(x); e != nil {
			return e
		}
	case SessionRead:
		if e := kindRights(x.Kind, x.Grant.Rights); e != nil {
			return e
		}
	case CodeSecret:
		if !duration(x.IssuedAt, x.ExpiresAt, 60) {
			return fmt.Errorf("code expiry")
		}
	case ExchangeSecret:
		if !duration(x.IssuedAt, x.IdleExpiresAt, 1800) || !duration(x.IssuedAt, x.ExpiresAt, 28800) {
			return fmt.Errorf("session expiry")
		}
	case CredentialSecret:
		if !duration(x.IssuedAt, x.ExpiresAt, 2592000) {
			return fmt.Errorf("credential expiry")
		}
		if e := kindRights(x.Kind, x.Rights); e != nil {
			return e
		}
	case CredentialCLI:
		if x.Outcome == "confirmed" {
			if x.Error != nil || x.Result == nil || x.Result.LocallyAvailable != (x.Command == "issue") {
				return fmt.Errorf("confirmed credential facts")
			}
			if x.Command == "issue" {
				r := x.Result
				if r.InstallationID == nil || r.BoardID == nil || r.Incarnation == nil || r.GrantID == nil || r.ActorID == nil || r.CredentialID == nil || r.IssuedAt == nil || r.ExpiresAt == nil {
					return fmt.Errorf("confirmed issue public facts")
				}
			}
		} else if x.Error == nil {
			return fmt.Errorf("credential diagnostic required")
		}
	case ExportStatus:
		if x.Result.Destination != nil {
			return fmt.Errorf("export destination must be null")
		}
		if (x.Outcome == "confirmed" || x.Outcome == "offered") && (x.Result.SourceIdentity == nil || x.Result.SourceRevision == nil) {
			return fmt.Errorf("export source facts")
		}
	case BoardView:
		if e := boardLinkTargets(x.Identity, x.Cards, x.Links); e != nil {
			return e
		}
		if e := readCoverage(x.Identity, x.Coverage); e != nil {
			return e
		}
		if x.Revision == 0 && (len(x.Cards) > 0 || len(x.Comments) > 0 || len(x.Links) > 0 || len(x.Attachments) > 0 || len(x.Approvals) > 0) {
			return fmt.Errorf("nonempty revision zero")
		}
		if e := current(x.Identity, x.Cards, x.Actors, x.Comments, x.Links, x.Attachments, x.Approvals, false); e != nil {
			return e
		}
	case CardView:
		if e := readCoverage(x.Identity, x.Coverage); e != nil {
			return e
		}
		if x.Revision == 0 {
			return fmt.Errorf("card revision zero")
		}
		if e := current(x.Identity, []A.Card{x.Card}, x.Actors, x.Comments, x.Links, x.Attachments, x.Approvals, true); e != nil {
			return e
		}
		prior := ""
		for _, r := range x.HistoryReferences {
			k := fmt.Sprintf("%s/%s/%s/%016d", r.Origin.BoardID, r.Origin.Incarnation, r.CardID, r.CardRevision)
			if k <= prior || r.Origin.BoardID != x.Identity.BoardID || r.CardID != x.Card.CardID || r.CardRevision >= x.Card.Revision {
				return fmt.Errorf("history reference scope/order")
			}
			prior = k
		}
	case HistoryView:
		if e := readCoverage(x.Identity, x.Coverage); e != nil {
			return e
		}
		if x.Revision == 0 {
			return fmt.Errorf("history revision zero")
		}
		if e := A.ValidateHistoryReferences(x.Identity, x.CardID, x.CardRevision, x.Records, A.Rows{Actors: x.Actors, Comments: x.Comments, Links: x.Links, Attachments: x.Attachments, Approvals: x.Approvals, Tombstones: x.Tombstones}); e != nil {
			return fmt.Errorf("invalid history references")
		}
	}
	if v.Type() == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if e := walk(v.Field(i)); e != nil {
				return e
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if e := walk(v.Index(i)); e != nil {
				return e
			}
		}
	}
	return nil
}

func readCoverage(identity A.Identity, coverage A.Coverage) error {
	for _, g := range coverage.Gaps {
		if g.Origin.BoardID != identity.BoardID {
			return fmt.Errorf("gap provenance board")
		}
	}
	return nil
}
func canonicalSecret(s string, n int) bool {
	b, e := base64.RawURLEncoding.Strict().DecodeString(s)
	return e == nil && len(b) == n && base64.RawURLEncoding.EncodeToString(b) == s
}
func duration(a, b A.Timestamp, n int) bool {
	x, e := time.Parse("2006-01-02T15:04:05Z", string(a))
	y, f := time.Parse("2006-01-02T15:04:05Z", string(b))
	return e == nil && f == nil && x.Add(time.Duration(n)*time.Second).Equal(y)
}
func kindRights(k A.Kind, r Rights) error {
	if k != "human" && k != "automation" {
		return fmt.Errorf("invalid kind")
	}
	if k == "automation" {
		for _, q := range r {
			if q == "approve" || q == "grant-admin" || q == "history-admin" {
				return fmt.Errorf("automation rights")
			}
		}
	}
	return nil
}

func terminal(x Terminal) error {
	if x.Retention == "full" {
		if x.Result == nil || x.Removal != nil {
			return fmt.Errorf("full terminal availability")
		}
	} else if x.Retention == "compact" {
		if x.Result != nil || x.Removal == nil {
			return fmt.Errorf("compact terminal availability")
		}
	} else {
		return fmt.Errorf("terminal retention")
	}
	class := A.TerminalClass("NOT_COMMITTED")
	if x.Outcome == "committed" {
		class = "COMMITTED"
	} else if x.Outcome != "not_committed" {
		return fmt.Errorf("terminal class")
	}
	// Validate the shared retained facts through C03's compact seam. This local
	// projection supplies no intent, verifies no fingerprint, and is never emitted.
	removal := x.Removal
	if removal == nil {
		removal = &A.Removal{Reason: "expired", RemovedAt: x.TerminalAt, BoardRevision: x.ExpectedRevision}
	}
	o := A.Operation{Origin: A.Identity{BoardID: x.BoardID, Incarnation: x.Incarnation}, OpID: x.OpID, ActorID: x.ActorID, FingerprintVersion: x.FingerprintVersion, Fingerprint: x.Fingerprint, Command: x.Command, Target: x.Target, ExpectedRevision: x.ExpectedRevision, ExpectedCardRevision: x.ExpectedCardRevision, TerminalClass: class, Code: x.Code, TerminalAt: x.TerminalAt, CommittedRevision: x.Revision, CommittedCardRevision: x.CardRevision, ResultRefs: x.ResultRefs, ByteFacts: x.ByteFacts, Removal: removal}
	if e := A.Validate(o); e != nil {
		return fmt.Errorf("invalid terminal facts")
	}
	if x.Result == nil {
		return nil
	}
	if e := contract.ValidateResult(*x.Result, contract.Command(x.Command), x.Outcome == "committed"); e != nil {
		return fmt.Errorf("invalid terminal result")
	}
	if x.Outcome == "not_committed" {
		r, e := Decode[RefusalResult](*x.Result)
		if e != nil || r.Error.Code != x.Code {
			return fmt.Errorf("terminal refusal binding")
		}
		return nil
	}
	var result map[string]json.RawMessage
	json.Unmarshal(*x.Result, &result)
	for key, raw := range result {
		if strings.HasSuffix(key, "Id") {
			var id A.UUID
			json.Unmarshal(raw, &id)
			kind := A.FactKind(strings.TrimSuffix(key, "Id"))
			found := false
			for _, ref := range x.ResultRefs {
				found = found || (ref.Kind == kind && ref.ID == id)
			}
			if !found {
				return fmt.Errorf("terminal result reference binding")
			}
		}
		if key == "coverageRevision" {
			var n A.Positive
			json.Unmarshal(raw, &n)
			if x.Revision == nil || n != *x.Revision {
				return fmt.Errorf("coverage revision binding")
			}
		}
	}
	return nil
}
