package archive

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Workflow is an immutable board-scoped policy. A nil workflow means the exact
// legacy profile; it must never be synthesized into an exported definition.
type Workflow struct {
	Profile     string               `json:"profile,omitempty"`
	WorkflowID  UUID                 `json:"workflowId"`
	Revision    PolicyVersion        `json:"revision"`
	Lanes       []WorkflowLane       `json:"lanes"`
	Transitions []WorkflowTransition `json:"transitions"`
	ActorRights WorkflowActorRights  `json:"actorRights"`
}
type WorkflowPolicy struct {
	PolicyVersion PolicyVersion `json:"policyVersion"`
	Workflow      *Workflow     `json:"workflow,omitempty"`
}

type WorkflowLane struct {
	ID    UUID   `json:"id"`
	Label string `json:"label"`
	Role  string `json:"role"`
}
type WorkflowTransition struct {
	From   UUID   `json:"from"`
	To     UUID   `json:"to"`
	Actors []Kind `json:"actors"`
}
type WorkflowActorRights struct {
	Human      []string `json:"human"`
	Automation []string `json:"automation"`
}

var ordinaryWorkflowRights = []string{"attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"}

// DecodeWorkflow validates the submitted byte bound as well as strict JSON and
// the complete definition. Explicit null never denotes the legacy profile.
func DecodeWorkflow(data []byte) (*Workflow, error) {
	if len(data) > 65536 {
		return nil, fmt.Errorf("workflow exceeds 64 KiB")
	}
	w, err := Decode[Workflow](data)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// ValidateWorkflow is shared by setup, storage, reads and archive validation.
// It never sorts, trims, deduplicates or otherwise repairs supplied policy.
func ValidateWorkflow(w *Workflow) error {
	if w == nil {
		return nil
	}
	fail := func() error { return fmt.Errorf("invalid workflow definition") }
	validID := func(id UUID) bool {
		return uuidRE.MatchString(string(id)) && id != "00000000-0000-0000-0000-000000000000"
	}
	if !validID(w.WorkflowID) || w.Revision < 1 || w.Revision > 9007199254740991 || (w.Profile != "" && w.Profile != "default") || (w.Profile == "default" && w.Revision < 2) || len(w.Lanes) < 4 || len(w.Lanes) > 16 || len(w.Transitions) < 3 || len(w.Transitions) > 64 {
		return fail()
	}
	lanes := map[UUID]string{}
	roles := map[string]UUID{}
	for i, l := range w.Lanes {
		if !(validID(l.ID) || (w.Profile == "default" && WorkflowRole(nil, Lane(l.ID)) == l.Role && l.Role != "waiting")) || lanes[l.ID] != "" || !utf8.ValidString(l.Label) || utf8.RuneCountInString(l.Label) < 1 || utf8.RuneCountInString(l.Label) > 80 || strings.TrimSpace(l.Label) != l.Label {
			return fail()
		}
		for _, r := range l.Label {
			if r < 32 || r == 127 {
				return fail()
			}
		}
		for _, prev := range w.Lanes[:i] {
			if strings.EqualFold(prev.Label, l.Label) {
				return fail()
			}
		}
		if w.Profile == "default" && l.Role != "waiting" && WorkflowRole(nil, Lane(l.ID)) != l.Role {
			return fail()
		}
		switch l.Role {
		case "initial", "in_progress", "review", "completed":
			if roles[l.Role] != "" {
				return fail()
			}
			roles[l.Role] = l.ID
		case "waiting":
		default:
			return fail()
		}
		lanes[l.ID] = l.Role
	}
	for _, role := range []string{"initial", "in_progress", "review", "completed"} {
		if roles[role] == "" {
			return fail()
		}
	}
	for _, kind := range []Kind{"human", "automation"} {
		rights := w.ActorRights.Automation
		if kind == "human" {
			rights = w.ActorRights.Human
		}
		if rights == nil {
			return fail()
		}
		for i, right := range rights {
			if i > 0 && rights[i-1] >= right {
				return fail()
			}
			allowed := kind == "human" && right == "approve"
			for _, ordinary := range ordinaryWorkflowRights {
				allowed = allowed || right == ordinary
			}
			if !allowed {
				return fail()
			}
		}
		required := []string{"read", "read-own-outcome"}
		if kind == "human" {
			required = append(required, "create", "edit", "review-handoff", "approve")
		}
		for _, right := range required {
			if !WorkflowAllowsRight(w, kind, right) {
				return fail()
			}
		}
	}
	forward, backward := map[UUID][]UUID{}, map[UUID][]UUID{}
	reviewEdges, completedEdges := 0, 0
	prior := ""
	for _, e := range w.Transitions {
		key := string(e.From) + "/" + string(e.To)
		if lanes[e.From] == "" || lanes[e.To] == "" || e.From == e.To || key <= prior || lanes[e.From] == "completed" || len(e.Actors) == 0 {
			return fail()
		}
		prior = key
		right := "edit"
		if lanes[e.To] == "review" {
			reviewEdges++
			right = "review-handoff"
			if lanes[e.From] != "in_progress" {
				return fail()
			}
		}
		if lanes[e.To] == "completed" {
			completedEdges++
			right = "approve"
			if lanes[e.From] != "review" || len(e.Actors) != 1 || e.Actors[0] != "human" {
				return fail()
			}
		}
		human := false
		for i, actor := range e.Actors {
			if (actor != "human" && actor != "automation") || (i > 0 && e.Actors[i-1] >= actor) || !WorkflowAllowsRight(w, actor, right) {
				return fail()
			}
			human = human || actor == "human"
		}
		if (right == "approve" || right == "review-handoff") && !human {
			return fail()
		}
		if human {
			forward[e.From] = append(forward[e.From], e.To)
			backward[e.To] = append(backward[e.To], e.From)
		}
	}
	if reviewEdges != 1 || completedEdges != 1 {
		return fail()
	}
	reachable := func(start UUID, edges map[UUID][]UUID) map[UUID]bool {
		seen := map[UUID]bool{start: true}
		queue := []UUID{start}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, next := range edges[current] {
				if !seen[next] {
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
		return seen
	}
	fromInitial, toCompleted := reachable(roles["initial"], forward), reachable(roles["completed"], backward)
	for id := range lanes {
		if !fromInitial[id] || !toCompleted[id] {
			return fail()
		}
	}
	b, err := Canonical(w)
	if err != nil || len(b) > 65536 {
		return fail()
	}
	return nil
}

// WorkflowRole returns empty for an unknown lane, never guessing from a label.
func WorkflowRole(w *Workflow, lane Lane) string {
	if w == nil {
		switch lane {
		case "Ready for Work":
			return "initial"
		case "In Progress":
			return "in_progress"
		case "Ready for Review":
			return "review"
		case "Completed":
			return "completed"
		}
		return ""
	}
	for _, l := range w.Lanes {
		if string(l.ID) == string(lane) {
			return l.Role
		}
	}
	return ""
}
func WorkflowLaneForRole(w *Workflow, role string) Lane {
	if w == nil {
		switch role {
		case "initial":
			return "Ready for Work"
		case "in_progress":
			return "In Progress"
		case "review":
			return "Ready for Review"
		case "completed":
			return "Completed"
		}
		return ""
	}
	for _, l := range w.Lanes {
		if l.Role == role {
			return Lane(l.ID)
		}
	}
	return ""
}
func WorkflowInitial(w *Workflow) Lane { return WorkflowLaneForRole(w, "initial") }
func WorkflowAllowsRight(w *Workflow, kind Kind, right string) bool {
	if kind != "human" && kind != "automation" {
		return false
	}
	if w == nil {
		if right == "approve" {
			return kind == "human"
		}
		for _, r := range ordinaryWorkflowRights {
			if r == right {
				return true
			}
		}
		return false
	}
	rights := w.ActorRights.Automation
	if kind == "human" {
		rights = w.ActorRights.Human
	}
	for _, r := range rights {
		if r == right {
			return true
		}
	}
	return false
}
func WorkflowAllows(w *Workflow, from, to Lane, kind Kind) bool {
	if kind != "human" && kind != "automation" {
		return false
	}
	if w == nil {
		return (from == "Ready for Work" && to == "In Progress") || (from == "In Progress" && to == "Ready for Review") || (from == "Ready for Review" && to == "Completed" && kind == "human")
	}
	for _, edge := range w.Transitions {
		if string(edge.From) == string(from) && string(edge.To) == string(to) {
			for _, actor := range edge.Actors {
				if actor == kind {
					return true
				}
			}
		}
	}
	return false
}
func WorkflowBytes(w *Workflow) ([]byte, error) {
	if err := ValidateWorkflow(w); err != nil {
		return nil, err
	}
	if w == nil {
		return nil, nil
	}
	return Canonical(w)
}

// ValidateWorkflowLanes applies profile membership to committed facts only.
// Attempted NOT_COMMITTED command inputs are deliberately outside this check.
func ValidateWorkflowLanes(w *Workflow, lanes ...Lane) error {
	if err := ValidateWorkflow(w); err != nil {
		return err
	}
	for _, lane := range lanes {
		if WorkflowRole(w, lane) == "" {
			return fmt.Errorf("lane outside workflow")
		}
	}
	return nil
}

// ValidateWorkflowFacts applies configured terminal approval invariants to the
// complete supplied current/history projection, before restore can allocate a
// target. Legacy historical interpretation is intentionally unchanged.
func ValidateWorkflowFacts(w *Workflow, cards []Card, records []Snapshot, actors []Actor, approvals []Approval) error {
	if err := ValidateWorkflow(w); err != nil {
		return err
	}
	if w == nil {
		return nil
	}
	kinds := make(map[UUID]Kind, len(actors))
	for _, actor := range actors {
		kinds[actor.ActorID] = actor.Kind
	}
	byID := make(map[UUID]Approval, len(approvals))
	for _, approval := range approvals {
		if kinds[approval.ActorID] != "human" || approval.PolicyVersion != 1 {
			return fmt.Errorf("invalid workflow approval actor or policy")
		}
		byID[approval.ApprovalID] = approval
	}
	state := func(cardID UUID, revision Positive, lane Lane, id *UUID) error {
		role := WorkflowRole(w, lane)
		if role == "" {
			return fmt.Errorf("lane outside workflow")
		}
		if role != "completed" {
			if id != nil {
				return fmt.Errorf("approval on noncompleted workflow state")
			}
			return nil
		}
		if id == nil {
			return fmt.Errorf("completed workflow state lacks approval")
		}
		approval, ok := byID[*id]
		if !ok || approval.CardID != cardID || revision < 2 || approval.ReviewedCardRevision != revision-1 {
			return fmt.Errorf("workflow approval does not match reviewed state")
		}
		return nil
	}
	for _, card := range cards {
		if err := state(card.CardID, card.Revision, card.Lane, card.CurrentApprovalID); err != nil {
			return err
		}
	}
	for _, record := range records {
		if record.State != nil {
			if err := state(record.CardID, record.CardRevision, record.State.Lane, record.State.CurrentApprovalID); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateInitialWorkflow preserves the configured-only revision-one setup grammar.
func ValidateInitialWorkflow(w *Workflow) error {
	if w != nil && (w.Profile != "" || w.Revision != 1) {
		return fmt.Errorf("invalid initial workflow")
	}
	return ValidateWorkflow(w)
}

// WorkflowAtVersion resolves only retained board policy, never current labels.
func WorkflowAtVersion(current *Workflow, policies []WorkflowPolicy, version PolicyVersion) (*Workflow, error) {
	if version < 1 || version > 9007199254740991 {
		return nil, fmt.Errorf("invalid policy version")
	}
	if policies == nil {
		if version != 1 || (current != nil && current.Revision != 1) {
			return nil, fmt.Errorf("missing retained policy")
		}
		return current, nil
	}
	if uint64(version) > uint64(len(policies)) || policies[int(version)-1].PolicyVersion != version {
		return nil, fmt.Errorf("missing retained policy")
	}
	return policies[int(version)-1].Workflow, nil
}

// ValidateWorkflowPolicies validates the immutable, contiguous policy history and
// stable identities. It does not infer occupancy or rewrite a retired identity.
func ValidateWorkflowPolicies(version PolicyVersion, current *Workflow, policies []WorkflowPolicy) error {
	if version < 1 || version > 9007199254740991 {
		return fmt.Errorf("invalid current policy")
	}
	if err := ValidateWorkflow(current); err != nil {
		return err
	}
	if policies == nil {
		if version != 1 || (current != nil && (current.Revision != 1 || current.Profile != "")) {
			return fmt.Errorf("missing retained policies")
		}
		return nil
	}
	if len(policies) < 2 || len(policies) > 128 || uint64(version) != uint64(len(policies)) {
		return fmt.Errorf("invalid policy history length")
	}
	encoded, err := Canonical(policies)
	if err != nil || len(encoded) > 8388608 {
		return fmt.Errorf("policy history exceeds capacity")
	}
	seen := map[UUID]bool{}
	retired := map[UUID]bool{}
	active := map[UUID]string{}
	var identity UUID
	profile := ""
	for i, p := range policies {
		if p.PolicyVersion != PolicyVersion(i+1) || (p.Workflow == nil && i != 0) {
			return fmt.Errorf("invalid policy history sequence")
		}
		if err := ValidateWorkflow(p.Workflow); err != nil {
			return err
		}
		if i == 0 && p.Workflow == nil {
			profile = "default"
			for _, role := range []string{"initial", "in_progress", "review", "completed"} {
				id := UUID(WorkflowLaneForRole(nil, role))
				active[id] = role
				seen[id] = true
			}
			continue
		}
		w := p.Workflow
		if w.Revision != p.PolicyVersion {
			return fmt.Errorf("workflow revision mismatch")
		}
		if i == 0 {
			profile = w.Profile
			if profile != "" {
				return fmt.Errorf("invalid first policy profile")
			}
			identity = w.WorkflowID
		}
		if i == 1 && policies[0].Workflow == nil {
			identity = w.WorkflowID
		}
		if w.Profile != profile || w.WorkflowID != identity {
			return fmt.Errorf("workflow identity changed")
		}
		next := map[UUID]string{}
		for _, lane := range w.Lanes {
			if retired[lane.ID] {
				return fmt.Errorf("retired lane reused")
			}
			if old, ok := active[lane.ID]; ok && old != lane.Role {
				return fmt.Errorf("lane role changed")
			}
			if seen[lane.ID] && active[lane.ID] == "" {
				return fmt.Errorf("lane identity reused")
			}
			next[lane.ID] = lane.Role
			seen[lane.ID] = true
		}
		for id, role := range active {
			if next[id] == "" {
				if role != "waiting" {
					return fmt.Errorf("required lane retired")
				}
				retired[id] = true
			}
		}
		active = next
	}
	expected, _ := Canonical(policies[len(policies)-1].Workflow)
	actual, _ := Canonical(current)
	if !bytes.Equal(expected, actual) {
		return fmt.Errorf("current workflow mismatch")
	}
	return nil
}

// ValidateWorkflowPolicyFacts keeps historical default-one acceptance while
// applying strict authority to every current card after an explicit edit.
func ValidateWorkflowPolicyFacts(version PolicyVersion, w *Workflow, policies []WorkflowPolicy, cards []Card, records []Snapshot, actors []Actor, approvals []Approval) error {
	if err := ValidateWorkflowPolicies(version, w, policies); err != nil {
		return err
	}
	kinds := map[UUID]Kind{}
	for _, actor := range actors {
		kinds[actor.ActorID] = actor.Kind
	}
	byID := map[UUID]Approval{}
	strictApproval := func(a Approval) error {
		issuing, err := WorkflowAtVersion(w, policies, a.PolicyVersion)
		if err != nil || kinds[a.ActorID] != "human" || !WorkflowAllowsRight(issuing, "human", "approve") || !WorkflowAllows(issuing, WorkflowLaneForRole(issuing, "review"), WorkflowLaneForRole(issuing, "completed"), "human") {
			return fmt.Errorf("invalid issuing approval policy")
		}
		return nil
	}
	for _, a := range approvals {
		issuing, err := WorkflowAtVersion(w, policies, a.PolicyVersion)
		if err != nil {
			return err
		}
		if issuing != nil {
			if err := strictApproval(a); err != nil {
				return err
			}
		}
		byID[a.ApprovalID] = a
	}
	state := func(cardID UUID, revision Positive, lane Lane, id *UUID, pin *PolicyVersion, current bool) error {
		v := PolicyVersion(1)
		if pin != nil && version == 1 {
			return fmt.Errorf("explicit pin before policy editing")
		}
		if pin != nil {
			v = *pin
		}
		policy, err := WorkflowAtVersion(w, policies, v)
		if err != nil {
			return err
		}
		if WorkflowRole(policy, lane) == "" {
			return fmt.Errorf("state lane outside recorded policy")
		}
		if current {
			policy = w
		}
		if !current && policy == nil {
			return nil
		}
		if current && w == nil {
			return nil
		}
		role := WorkflowRole(policy, lane)
		if role == "" {
			return fmt.Errorf("lane outside current policy")
		}
		if role != "completed" {
			if id != nil {
				return fmt.Errorf("approval on noncompleted state")
			}
			return nil
		}
		if id == nil {
			return fmt.Errorf("completed state lacks approval")
		}
		a, ok := byID[*id]
		if !ok || a.CardID != cardID || revision < 2 || a.ReviewedCardRevision != revision-1 || a.PolicyVersion > v {
			return fmt.Errorf("approval reviewed state mismatch")
		}
		return strictApproval(a)
	}
	for _, card := range cards {
		if err := state(card.CardID, card.Revision, card.Lane, card.CurrentApprovalID, card.WorkflowPolicyVersion, true); err != nil {
			return err
		}
	}
	for _, r := range records {
		if r.State != nil {
			if err := state(r.CardID, r.CardRevision, r.State.Lane, r.State.CurrentApprovalID, r.State.WorkflowPolicyVersion, false); err != nil {
				return err
			}
		}
	}
	return nil
}
