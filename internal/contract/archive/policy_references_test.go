package archive

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/localswim/localswim/internal/contract"
)

func TestWorkflowPolicyOriginalFullAndCompactBinding(t *testing.T) {
	o := fixture[Outcomes](t, "package/outcomes.json").Operations[0]
	w := testWorkflow()
	w.Revision = 2
	input := map[string]any{"expectedPolicyVersion": 1, "workflow": w}
	o.Command = "workflow.edit"
	o.Target = nil
	o.ExpectedCardRevision = nil
	o.CommittedCardRevision = nil
	o.ByteFacts = []ByteFact{}
	raw, err := json.Marshal(map[string]any{"opId": o.OpID, "incarnation": o.Origin.Incarnation, "target": nil, "expectedRevision": o.ExpectedRevision, "expectedCardRevision": nil, "input": input})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := contract.Freeze(raw, contract.ResolvedRoute{Command: "workflow.edit", BoardID: string(o.Origin.BoardID)}, contract.AuthenticatedActor{ActorID: string(o.ActorID)})
	if err != nil {
		t.Fatal(err)
	}
	version := Number(2)
	o.Fingerprint = Digest(frozen.Fingerprint())
	o.ResultRefs = []FactRef{{Kind: "workflow", ID: w.WorkflowID, Revision: &version}}
	result, _ := json.Marshal(map[string]any{"workflowId": w.WorkflowID, "policyVersion": 2})
	o.Full = &FullOutcome{Intent: frozen.CanonicalBytes(), Result: result}
	if err = Validate(o); err != nil {
		t.Fatal(err)
	}
	encoded, _ := Readable(o)
	decoded, err := DecodeReadable[Operation](encoded)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := Canonical(decoded.Full.Intent)
	if !bytes.Equal(canonical, frozen.CanonicalBytes()) {
		t.Fatal("original canonical bytes changed")
	}
	wrong := Number(3)
	decoded.ResultRefs[0].Revision = &wrong
	if err = Validate(decoded); err == nil {
		t.Fatal("full policy result mismatch accepted")
	}
	removed := Removal{Reason: "expired", RemovedAt: correctionTime, BoardRevision: Number(*o.CommittedRevision)}
	o.Full = nil
	o.Removal = &removed
	if err = Validate(o); err != nil {
		t.Fatal(err)
	}
	o.ResultRefs[0].Revision = nil
	if err = Validate(o); err == nil {
		t.Fatal("compact policy result without version accepted")
	}
}
