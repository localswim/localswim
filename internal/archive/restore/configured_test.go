package restore

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	V "github.com/localswim/localswim/internal/archive/validate"
	A "github.com/localswim/localswim/internal/contract/archive"
)

const configuredDefinition = `{"workflowId":"60000000-0000-4000-8000-000000000010","revision":1,"lanes":[{"id":"60000000-0000-4000-8000-000000000001","label":"Intake 雪","role":"initial"},{"id":"60000000-0000-4000-8000-000000000005","label":"Waiting é","role":"waiting"},{"id":"60000000-0000-4000-8000-000000000002","label":"Active","role":"in_progress"},{"id":"60000000-0000-4000-8000-000000000003","label":"Check","role":"review"},{"id":"60000000-0000-4000-8000-000000000004","label":"Accepted","role":"completed"}],"transitions":[{"from":"60000000-0000-4000-8000-000000000001","to":"60000000-0000-4000-8000-000000000002","actors":["automation","human"]},{"from":"60000000-0000-4000-8000-000000000001","to":"60000000-0000-4000-8000-000000000005","actors":["human"]},{"from":"60000000-0000-4000-8000-000000000002","to":"60000000-0000-4000-8000-000000000003","actors":["automation","human"]},{"from":"60000000-0000-4000-8000-000000000003","to":"60000000-0000-4000-8000-000000000004","actors":["human"]},{"from":"60000000-0000-4000-8000-000000000005","to":"60000000-0000-4000-8000-000000000002","actors":["human"]}],"actorRights":{"human":["approve","attach","comment","create","edit","export","read","read-own-outcome","review-handoff"],"automation":["create","edit","read","read-own-outcome","review-handoff"]}}`

func configuredMembers(t *testing.T) map[string][]byte {
	t.Helper()
	ms := fixture(t)
	b, err := A.DecodeReadable[A.Board](ms["board.json"])
	if err != nil {
		t.Fatal(err)
	}
	w, err := A.Decode[A.Workflow]([]byte(configuredDefinition))
	if err != nil {
		t.Fatal(err)
	}
	b.Workflow = &w
	for i := range b.Cards {
		b.Cards[i].Lane = A.Lane(w.Lanes[0].ID)
	}
	h, err := A.DecodeReadable[A.History](ms["history.json"])
	if err != nil {
		t.Fatal(err)
	}
	for i := range h.Records {
		if h.Records[i].State != nil {
			h.Records[i].State.Lane = A.Lane(w.Lanes[0].ID)
		}
	}
	ms["board.json"], err = A.Readable(b)
	if err != nil {
		t.Fatal(err)
	}
	ms["history.json"], err = A.Readable(h)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func TestConfiguredPreparedConservesDefinitionAndHistoricalFacts(t *testing.T) {
	source := sourcePackage(t, configuredMembers(t))
	b, _ := decode[A.Board](source, "board.json")
	h, _ := decode[A.History](source, "history.json")
	o, _ := decode[A.Outcomes](source, "outcomes.json")
	p, err := prepare(context.Background(), source, testTime, testInc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.board.Workflow, b.Workflow) || !reflect.DeepEqual(p.board.Cards, b.Cards) || !reflect.DeepEqual(p.history.Records, h.Records) || !reflect.DeepEqual(p.board.Approvals, b.Approvals) {
		t.Fatal("configured facts changed during identity transform")
	}
	for _, g := range p.outcomes.SourceArchives {
		if g.Identity == o.Identity && !reflect.DeepEqual(g.Operations, o.Operations) {
			t.Fatal("original bytes or terminal results changed")
		}
	}
	legacy, err := prepare(context.Background(), sourcePackage(t, fixture(t)), testTime, testInc)
	if err != nil || legacy.board.Workflow != nil || bytes.Contains(legacy.documents["board.json"], []byte(`"workflow"`)) {
		t.Fatal("legacy workflow must remain omitted", err)
	}
}

func TestConfiguredPackageRefusesMixedAndCorruptPolicyBeforePrepare(t *testing.T) {
	for _, mode := range []string{"missing", "null", "foreign-current", "foreign-history", "legacy-history", "duplicate-label", "automation-approve", "invalid-edge"} {
		t.Run(mode, func(t *testing.T) {
			ms := configuredMembers(t)
			var b, h map[string]any
			if err := json.Unmarshal(ms["board.json"], &b); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(ms["history.json"], &h); err != nil {
				t.Fatal(err)
			}
			w := b["workflow"].(map[string]any)
			switch mode {
			case "missing":
				delete(b, "workflow")
			case "null":
				b["workflow"] = nil
			case "foreign-current":
				b["cards"].([]any)[0].(map[string]any)["lane"] = "77777777-7777-4777-8777-777777777777"
			case "foreign-history":
				h["records"].([]any)[0].(map[string]any)["state"].(map[string]any)["lane"] = "77777777-7777-4777-8777-777777777777"
			case "legacy-history":
				h["records"].([]any)[0].(map[string]any)["state"].(map[string]any)["lane"] = "Ready for Work"
			case "duplicate-label":
				w["lanes"].([]any)[1].(map[string]any)["label"] = "INTAKE 雪"
			case "automation-approve":
				w["actorRights"].(map[string]any)["automation"] = []string{"approve", "create", "edit", "read", "read-own-outcome", "review-handoff"}
			case "invalid-edge":
				w["transitions"].([]any)[3].(map[string]any)["actors"] = []string{"automation", "human"}
			}
			ms["board.json"], _ = A.Readable(b)
			ms["history.json"], _ = A.Readable(h)
			if p, err := V.Read(context.Background(), bytes.NewReader(sourceArchiveBytes(t, ms))); err == nil || p != nil {
				t.Fatal("invalid configured package accepted")
			}
		})
	}
}
