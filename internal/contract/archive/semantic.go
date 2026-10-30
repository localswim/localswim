package archive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/localswim/localswim/internal/contract"
)

func hash(b []byte) Digest { x := sha256.Sum256(b); return Digest(hex.EncodeToString(x[:])) }
func equal(a, b any) bool {
	x, e := Canonical(a)
	y, f := Canonical(b)
	return e == nil && f == nil && bytes.Equal(x, y)
}
func idkey(i Identity) string { return string(i.BoardID) + "/" + string(i.Incarnation) }
func num(n uint64) string     { return fmt.Sprintf("%016d", n) }
func optionalTime(t *Timestamp) string {
	if t == nil {
		return ""
	}
	return string(*t)
}
func optionalNumber(n *Number) string {
	if n == nil {
		return ""
	}
	return num(uint64(*n))
}
func gapkey(g GapSelection) string {
	return idkey(g.Origin) + "/" + string(g.Class) + "/" + optionalTime(g.FromExclusive) + "/" + string(g.ThroughInclusive) + "/" + string(g.Reason)
}
func rowkey(v any) (string, bool) {
	switch x := v.(type) {
	case Actor:
		return string(x.ActorID), true
	case Card:
		return num(uint64(x.Ticket)) + "/" + string(x.CardID), true
	case Comment:
		return string(x.CommentID), true
	case Link:
		return string(x.LinkID), true
	case Attachment:
		return string(x.AttachmentID), true
	case Approval:
		return string(x.ApprovalID), true
	case EntityTombstone:
		return string(x.Kind) + "/" + string(x.ID), true
	case Entry:
		return x.Path, true
	case ByteFact:
		return string(x.SHA256), true
	case Lineage:
		return idkey(x.Identity) + "/" + string(x.ManifestSHA256) + "/" + num(uint64(x.Revision)), true
	case Snapshot:
		return idkey(x.Origin) + "/" + string(x.CardID) + "/" + num(uint64(x.CardRevision)), true
	case HistoryKey:
		return idkey(x.Origin) + "/" + string(x.CardID) + "/" + num(uint64(x.CardRevision)), true
	case Event:
		return idkey(x.Origin) + "/" + num(uint64(x.EventID)), true
	case ActivityKey:
		return idkey(x.Origin) + "/" + num(uint64(x.EventID)), true
	case OutcomeKey:
		return idkey(x.Origin) + "/" + string(x.OpID), true
	case Operation:
		return string(x.OpID), true
	case SourceArchive:
		return idkey(x.Identity), true
	case FactRef:
		return string(x.Kind) + "/" + string(x.ID) + "/" + optionalNumber(x.Revision), true
	case GapSelection:
		return gapkey(x), true
	case Gap:
		return gapkey(GapSelection{x.Origin, x.Class, x.Reason, x.FromExclusive, x.ThroughInclusive}) + "/" + num(uint64(x.BoardRevision)) + "/" + string(x.RecordedAt), true
	}
	return "", false
}

func semantics(v reflect.Value) error {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		return semantics(v.Elem())
	}
	if v.Type() == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	switch v.Kind() {
	case reflect.Slice:
		prior := ""
		for i := 0; i < v.Len(); i++ {
			x := v.Index(i)
			if k, ok := rowkey(x.Interface()); ok {
				if i > 0 && k <= prior {
					return fmt.Errorf("%s order/duplicate", x.Type())
				}
				prior = k
			}
			if e := semantics(x); e != nil {
				return e
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if e := semantics(v.Field(i)); e != nil {
				return e
			}
		}
	}
	switch x := v.Interface().(type) {
	case ByteFact:
		if x.ByteLength > 10485760 {
			return fmt.Errorf("byte fact length")
		}
	case Entry:
		if !inventoryPath(x.Path) || (strings.HasPrefix(x.Path, "blobs/") && (x.Path != "blobs/sha256-"+string(x.SHA256) || x.ByteLength > 10485760)) {
			return fmt.Errorf("inventory entry scope")
		}
	case Attachment:
		if x.ByteLength > 10485760 {
			return fmt.Errorf("attachment length")
		}
	case EntityTombstone:
		if (x.Kind == "comment") != (x.ActorID != nil) || (x.Kind == "card") != (x.CreatedAt == nil) || (x.Kind == "card" && x.ID != x.CardID) {
			return fmt.Errorf("tombstone attribution")
		}
	case Snapshot:
		if err := ValidateHistoricalParent(x); err != nil {
			return err
		}
		if (x.State == nil) == (x.Removal == nil) {
			return fmt.Errorf("snapshot availability")
		}
	case Event:
		found := x.Type == "recovery.expire"
		for _, c := range commandValues {
			found = found || x.Type == c
		}
		if !found || (x.Type == "recovery.expire") != (x.OpID == nil) {
			return fmt.Errorf("event command/opId")
		}
	case Gap:
		if x.FromExclusive != nil && *x.FromExclusive > x.ThroughInclusive {
			return fmt.Errorf("gap interval")
		}
		if x.Reason == "cleared" && (x.Class == "activity" || x.FromExclusive != nil) {
			return fmt.Errorf("clear gap scope")
		}
	case GapSelection:
		if x.FromExclusive != nil && *x.FromExclusive > x.ThroughInclusive {
			return fmt.Errorf("gap interval")
		}
		if x.Reason == "cleared" && (x.Class == "activity" || x.FromExclusive != nil) {
			return fmt.Errorf("clear gap scope")
		}
	case Manifest:
		if e := inventory(x.Inventory); e != nil {
			return e
		}
		raw, e := Readable(x)
		if e != nil {
			return e
		}
		total := uint64(len(raw))
		for _, entry := range x.Inventory {
			if !strings.HasPrefix(entry.Path, "blobs/") {
				total += uint64(entry.ByteLength)
			}
		}
		if len(raw) > 33554432 || total > 67108864 {
			return fmt.Errorf("manifest/aggregate JSON limit")
		}
		for _, l := range x.Lineage {
			if l.Identity.BoardID != x.Source.BoardID {
				return fmt.Errorf("lineage board")
			}
		}
	case TargetInventory:
		if e := inventory(x.Entries); e != nil {
			return e
		}
		if x.EntryCount != Number(len(x.Entries)) {
			return fmt.Errorf("entry count")
		}
		b, e := Canonical(x.Entries)
		if e != nil {
			return e
		}
		if hash(b) != x.SHA256 {
			return fmt.Errorf("target inventory hash")
		}
	case Operation:
		return operation(x)
	case SourceArchive:
		for _, o := range x.Operations {
			if o.Origin != x.Identity {
				return fmt.Errorf("source operation origin")
			}
		}
	case Outcomes:
		for _, o := range x.Operations {
			if o.Origin != x.Identity {
				return fmt.Errorf("current operation origin")
			}
		}
		for _, s := range x.SourceArchives {
			if s.Identity == x.Identity || s.Identity.BoardID != x.Identity.BoardID {
				return fmt.Errorf("source archive identity")
			}
		}
	case PreviewRequest:
		return recoveryInput(x.Command, x.Input)
	case Confirmation:
		if x.Target != nil || x.ExpectedCardRevision != nil {
			return fmt.Errorf("recovery target/precondition")
		}
		return recoveryInput(x.Command, x.Input)
	case Preview:
		if e := recoveryInput(x.Command, x.Input); e != nil {
			return e
		}
		a, _ := time.Parse("2006-01-02T15:04:05Z", string(x.PreviewedAt))
		b, _ := time.Parse("2006-01-02T15:04:05Z", string(x.ExpiresAt))
		if !a.Add(300 * time.Second).Equal(b) {
			return fmt.Errorf("preview expiry")
		}
		if uint64(x.ExpectedRevision) == contract.MaxInteger || x.Inventory.ProposedBoardRevision != Positive(x.ExpectedRevision+1) {
			return fmt.Errorf("preview proposed revision")
		}
		m, e := jsonValue(x)
		if e != nil {
			return e
		}
		delete(m.(map[string]any), "previewHash")
		c, e := contract.Canonical(m)
		if e != nil {
			return e
		}
		if hash(c) != x.PreviewHash {
			return fmt.Errorf("preview original-time hash")
		}
		if x.Command == "history.clear" {
			var i struct {
				ThroughTime Timestamp `json:"throughTime"`
			}
			json.Unmarshal(x.Input, &i)
			if i.ThroughTime > x.PreviewedAt || x.Inventory.ProposedRetentionVersion != x.RetentionVersion {
				return fmt.Errorf("clear preview")
			}
		} else {
			var r Retention
			json.Unmarshal(x.Input, &r)
			if !equal(r, x.Inventory.ProposedRetention) || uint64(x.RetentionVersion) == contract.MaxInteger || x.Inventory.ProposedRetentionVersion != x.RetentionVersion+1 {
				return fmt.Errorf("retention preview")
			}
		}
	case PreviewInventory:
		if x.Counts.HistorySnapshots != Number(len(x.History)) || x.Counts.ActivityEvents != Number(len(x.Activity)) || x.Counts.OutcomePayloads != Number(len(x.Outcomes)) {
			return fmt.Errorf("preview counts")
		}
	case PreviewBytes:
		return previewBytes(x)
	case Receipt:
		b, e := Readable(x.SourceManifest)
		if e != nil {
			return e
		}
		if hash(b) != x.SourceManifestSHA256 {
			return fmt.Errorf("receipt manifest hash")
		}
		if x.Destination.BoardID != x.SourceManifest.Source.BoardID || x.Destination.Incarnation == x.SourceManifest.Source.Incarnation {
			return fmt.Errorf("fresh destination")
		}
		for _, l := range x.SourceManifest.Lineage {
			if l.Identity.Incarnation == x.Destination.Incarnation {
				return fmt.Errorf("lineage destination")
			}
		}
		if x.UpdatedAt < x.CreatedAt {
			return fmt.Errorf("receipt chronology")
		}
		if (x.Result == "failed") != (x.FailureCode != nil) || (x.FailureCode != nil && *x.FailureCode == "") {
			return fmt.Errorf("receipt failure code")
		}
		if (x.Phase == "preparing") != (x.TargetInventory == nil) {
			return fmt.Errorf("receipt phase anchor")
		}
		if (x.Result == "complete") != (x.Phase == "published") {
			return fmt.Errorf("receipt completion")
		}
	case Status:
		if x.Outcome == "confirmed" && (x.Result.SourceIdentity == nil || x.Result.SourceRevision == nil || x.Result.Destination == nil) {
			return fmt.Errorf("confirmed status facts")
		}
	}
	return nil
}
func inventoryPath(p string) bool {
	if p == "board.json" || p == "history.json" || p == "activity.json" || p == "outcomes.json" {
		return true
	}
	return strings.HasPrefix(p, "blobs/sha256-") && digestRE.MatchString(strings.TrimPrefix(p, "blobs/sha256-"))
}
func inventory(es []Entry) error {
	seen := map[string]bool{}
	var blobs uint64
	var jsonBytes uint64
	for _, e := range es {
		seen[e.Path] = true
		if strings.HasPrefix(e.Path, "blobs/") {
			blobs += uint64(e.ByteLength)
		} else if e.ByteLength > 33554432 {
			return fmt.Errorf("JSON size")
		} else {
			jsonBytes += uint64(e.ByteLength)
		}
	}
	if len(es) > 16382 || blobs > 67108864 || jsonBytes > 67108864 {
		return fmt.Errorf("inventory limit")
	}
	for _, p := range []string{"board.json", "history.json", "activity.json", "outcomes.json"} {
		if !seen[p] {
			return fmt.Errorf("missing inventory payload")
		}
	}
	return nil
}

func recoveryInput(c RecoveryCommand, b []byte) error {
	switch c {
	case "retention.set":
		_, e := Decode[Retention](b)
		return e
	case "history.clear":
		_, e := Decode[struct {
			ThroughTime Timestamp `json:"throughTime"`
		}](b)
		return e
	}
	return fmt.Errorf("recovery command")
}

func previewBytes(x PreviewBytes) error {
	lengths := map[Digest]Number{}
	sum := func(a []ByteFact) (Number, error) {
		var n uint64
		for _, f := range a {
			if f.ByteLength > 10485760 {
				return 0, fmt.Errorf("BLOB length")
			}
			if old, ok := lengths[f.SHA256]; ok && old != f.ByteLength {
				return 0, fmt.Errorf("BYTE_LENGTH_CONFLICT")
			}
			lengths[f.SHA256] = f.ByteLength
			n += uint64(f.ByteLength)
			if n > contract.MaxInteger {
				return 0, fmt.Errorf("byte sum overflow")
			}
		}
		return Number(n), nil
	}
	for i, a := range [][]ByteFact{x.DomainBefore, x.DomainAfter, x.ExportPinned, x.Reclaimable} {
		n, e := sum(a)
		if e != nil {
			return e
		}
		if n != []Number{x.DomainBeforeBytes, x.DomainAfterBytes, x.ExportPinnedBytes, x.ReclaimableBytes}[i] {
			return fmt.Errorf("preview byte sum")
		}
	}
	before := map[Digest]bool{}
	for _, f := range x.DomainBefore {
		before[f.SHA256] = true
	}
	for _, f := range x.DomainAfter {
		if !before[f.SHA256] {
			return fmt.Errorf("domainAfter is not a removal-only subset")
		}
	}
	after := map[Digest]bool{}
	for _, a := range [][]ByteFact{x.DomainAfter, x.ExportPinned} {
		for _, f := range a {
			after[f.SHA256] = true
		}
	}
	want := []ByteFact{}
	for _, f := range x.DomainBefore {
		if !after[f.SHA256] {
			want = append(want, f)
		}
	}
	if !equal(want, x.Reclaimable) {
		return fmt.Errorf("reclaimable set")
	}
	return nil
}

func operation(o Operation) error {
	card := false
	grant := false
	known := false
	for _, d := range contract.Commands() {
		if string(d.Command) == string(o.Command) {
			known = true
			card = d.Target == "card"
			grant = d.Target == "grant"
		}
	}
	if !known {
		return fmt.Errorf("unsupported command")
	}
	if card {
		if o.Target == nil || o.Target.CardID == nil || o.Target.GrantID != nil || o.ExpectedCardRevision == nil {
			return fmt.Errorf("card target/precondition")
		}
	} else if grant {
		if o.Target == nil || o.Target.GrantID == nil || o.Target.CardID != nil || o.ExpectedCardRevision != nil {
			return fmt.Errorf("grant target/precondition")
		}
	} else if o.Target != nil || o.ExpectedCardRevision != nil {
		return fmt.Errorf("null target/precondition")
	}
	if (o.Full == nil) == (o.Removal == nil) {
		return fmt.Errorf("outcome availability")
	}
	committed := o.TerminalClass == "COMMITTED"
	cardMutation := card || o.Command == "card.create"
	if committed {
		if o.Code != "OK" || o.CommittedRevision == nil || (o.CommittedCardRevision != nil) != cardMutation {
			return fmt.Errorf("commit facts")
		}
	} else if o.Code == "" || o.Code == "OK" || o.CommittedRevision != nil || o.CommittedCardRevision != nil || len(o.ResultRefs) != 0 {
		return fmt.Errorf("REFUSAL_EFFECT")
	}
	if committed {
		if e := compactReferences(o); e != nil {
			return e
		}
	}
	want := 0
	if o.Command == "attachment.add" {
		want = 1
	}
	if len(o.ByteFacts) != want {
		return fmt.Errorf("BYTE_FACTS_CARDINALITY")
	}
	for _, f := range o.ByteFacts {
		if f.ByteLength > 10485760 {
			return fmt.Errorf("byte fact length")
		}
	}
	if o.Full == nil {
		return nil
	}
	if e := contract.ValidateResult(o.Full.Result, contract.Command(o.Command), committed); e != nil {
		return e
	}
	raw, e := contract.DecodeJSON(o.Full.Intent)
	if e != nil {
		return e
	}
	m, ok := raw.(map[string]any)
	if !ok || len(m) != 10 {
		return fmt.Errorf("intent shape")
	}
	for k, v := range map[string]any{"fingerprintVersion": Version(1), "boardId": o.Origin.BoardID, "incarnation": o.Origin.Incarnation, "opId": o.OpID, "actorId": o.ActorID, "command": o.Command, "target": o.Target, "expectedRevision": o.ExpectedRevision, "expectedCardRevision": o.ExpectedCardRevision} {
		if _, ok := m[k]; !ok {
			return fmt.Errorf("intent missing %s", k)
		}
		if !equal(m[k], v) {
			return fmt.Errorf("intent %s binding", k)
		}
	}
	payload, ok := m["payload"].(map[string]any)
	if !ok {
		return fmt.Errorf("intent payload")
	}
	wire := map[string]any{"opId": m["opId"], "incarnation": m["incarnation"], "target": m["target"], "expectedRevision": m["expectedRevision"], "expectedCardRevision": m["expectedCardRevision"]}
	var desc contract.CommandDescriptor
	for _, d := range contract.Commands() {
		if string(d.Command) == string(o.Command) {
			desc = d
		}
	}
	if desc.Recovery {
		wire["input"] = payload["input"]
		wire["previewHash"] = payload["previewHash"]
		wire["command"] = string(o.Command)
	} else {
		input := map[string]any{}
		for k, v := range payload {
			if f, ok := desc.Input.Fields[k]; ok && f.Optional && v == nil && k != "ownerId" && k != "archived" && k != "parent" {
				continue
			}
			input[k] = v
		}
		wire["input"] = input
	}
	if strings.HasPrefix(string(o.Command), "grant.") {
		wire["boardId"] = o.Origin.BoardID
	}
	w, e := Canonical(wire)
	if e != nil {
		return e
	}
	targetID := ""
	if card {
		targetID = string(*o.Target.CardID)
	}
	if grant {
		targetID = string(*o.Target.GrantID)
	}
	frozen, e := contract.Freeze(w, contract.ResolvedRoute{Command: contract.Command(o.Command), BoardID: string(o.Origin.BoardID), TargetID: targetID}, contract.AuthenticatedActor{ActorID: string(o.ActorID)})
	if e != nil {
		return e
	}
	c, e := contract.Canonical(m)
	if e != nil {
		return e
	}
	if !bytes.Equal(c, frozen.CanonicalBytes()) || Digest(frozen.Fingerprint()) != o.Fingerprint {
		return fmt.Errorf("FINGERPRINT")
	}
	if o.Command == "attachment.add" {
		f := o.ByteFacts[0]
		if !equal(payload["sha256"], f.SHA256) || !equal(payload["byteLength"], f.ByteLength) {
			return fmt.Errorf("BYTE_FACTS_MAPPING")
		}
	}
	if !committed {
		var r struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		json.Unmarshal(o.Full.Result, &r)
		if r.Error.Code != o.Code {
			return fmt.Errorf("refusal code binding")
		}
	} else if e := fullReferences(o); e != nil {
		return e
	}
	return nil
}

func compactReferences(o Operation) error {
	kinds := []string{"card"}
	switch o.Command {
	case "comment.add":
		kinds = append(kinds, "comment")
	case "attachment.add":
		kinds = append(kinds, "attachment")
	case "link.add", "link.remove":
		kinds = append(kinds, "link")
	case "approve":
		kinds = append(kinds, "approval")
	case "grant.create", "grant.revoke":
		kinds = []string{"grant"}
	case "history.clear", "retention.set":
		kinds = []string{"board"}
	}
	if len(o.ResultRefs) != len(kinds) {
		return fmt.Errorf("result reference cardinality")
	}
	seen := map[string]bool{}
	for _, f := range o.ResultRefs {
		k := string(f.Kind)
		allowed := false
		for _, v := range kinds {
			allowed = allowed || v == k
		}
		if !allowed || seen[k] {
			return fmt.Errorf("result reference kinds")
		}
		seen[k] = true
		switch k {
		case "card":
			if f.Revision == nil || *f.Revision != Number(*o.CommittedCardRevision) {
				return fmt.Errorf("result card revision")
			}
			if o.Target != nil && o.Target.CardID != nil && f.ID != *o.Target.CardID {
				return fmt.Errorf("result card target")
			}
		case "board":
			if f.ID != o.Origin.BoardID || f.Revision == nil || *f.Revision != Number(*o.CommittedRevision) {
				return fmt.Errorf("result board revision")
			}
		default:
			if f.Revision != nil {
				return fmt.Errorf("result immutable revision")
			}
			if k == "grant" && o.Target != nil && o.Target.GrantID != nil && f.ID != *o.Target.GrantID {
				return fmt.Errorf("result grant target")
			}
		}
	}
	return nil
}
func fullReferences(o Operation) error {
	var result map[string]json.RawMessage
	if e := json.Unmarshal(o.Full.Result, &result); e != nil {
		return e
	}
	for key, kind := range map[string]FactKind{"cardId": "card", "commentId": "comment", "attachmentId": "attachment", "linkId": "link", "approvalId": "approval", "grantId": "grant"} {
		if raw, ok := result[key]; ok {
			var id UUID
			json.Unmarshal(raw, &id)
			found := false
			for _, f := range o.ResultRefs {
				found = found || (f.Kind == kind && f.ID == id)
			}
			if !found {
				return fmt.Errorf("result reference binding")
			}
		}
	}
	if raw, ok := result["coverageRevision"]; ok {
		var rev Number
		json.Unmarshal(raw, &rev)
		if rev != Number(*o.CommittedRevision) {
			return fmt.Errorf("coverage revision binding")
		}
	}
	return nil
}

// ValidateBytes compares an already supplied in-memory raw byte slice with its
// declared fact. Reading, streaming, allocation limits and publication stay with
// the package/native implementation; this helper cannot establish their safety.
func ValidateBytes(raw []byte, fact ByteFact) error {
	if e := Validate(fact); e != nil {
		return e
	}
	if fact.ByteLength > 10485760 || uint64(len(raw)) != uint64(fact.ByteLength) || hash(raw) != fact.SHA256 {
		return fmt.Errorf("byte fact mismatch")
	}
	return nil
}
