package v2

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func assertNoResponseValidators(t *testing.T, data []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	var inspect func(any)
	inspect = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				if strings.Contains(strings.ToLower(key), "etag") {
					t.Fatalf("response contains transport field %q", key)
				}
				inspect(child)
			}
		case []any:
			for _, child := range value {
				inspect(child)
			}
		case string:
			if snapshotETagPattern.MatchString(value) {
				t.Fatal("response embeds a transport validator value")
			}
			// Audit payloads are JSON encoded inside strings. Inspect those too.
			if strings.HasPrefix(value, "{") {
				var child any
				if json.Unmarshal([]byte(value), &child) == nil {
					inspect(child)
				}
			}
		}
	}
	inspect(value)
}

func TestEntityRevisionsReplaceResponseAndNativeValidators(t *testing.T) {
	app, editor := eventHTTPServer(t)
	raw, headers := editor.call("POST", "/boards", `{"name":"Revisions"}`, "", 201)
	assertNoResponseValidators(t, raw)
	b := turnOneDecode[board](t, raw)
	if b.Revision != 1 || headers.Get("ETag") != etag(b.ID, 1) {
		t.Fatal("board revision and HTTP validator disagree")
	}
	for _, resource := range []string{"users", "priorities"} {
		raw, _ = editor.call("GET", "/"+resource+"?board_id="+b.ID, "", "", 200)
		assertNoResponseValidators(t, raw)
		page := turnOnePage[map[string]any](t, raw, resource)
		if len(page.Values) == 0 {
			t.Fatal("expected entity collection")
		}
		for _, item := range page.Values {
			if item["revision"] != float64(1) {
				t.Fatal("immutable entity lacks initial numeric revision")
			}
		}
	}
	raw, headers = editor.call("GET", "/policies/"+b.PolicyID, "", "", 200)
	assertNoResponseValidators(t, raw)
	p := turnOneDecode[policy](t, raw)
	if p.Revision != 1 || headers.Get("ETag") != etag(p.ID, p.Revision) {
		t.Fatal("policy revision and HTTP validator disagree")
	}
	raw, _ = editor.call("GET", "/boards", "", "", 200)
	assertNoResponseValidators(t, raw)
	summaries := turnOnePage[boardSummary](t, raw, "boards")
	if len(summaries.Values) != 1 || summaries.Values[0].Revision == nil || *summaries.Values[0].Revision != 1 {
		t.Fatal("catalog lacks accepted board revision")
	}
	raw, headers = editor.call("GET", "/service", "", "", 200)
	assertNoResponseValidators(t, raw)
	service := turnOneDecode[map[string]any](t, raw)
	if service["revision"] != nil || headers.Get("ETag") == "" {
		t.Fatal("service must retain only its HTTP validator")
	}
	raw, _ = editor.call("GET", "/workspace", "", "", 200)
	assertNoResponseValidators(t, raw)
	raw, _ = editor.call("GET", "/snapshots/"+b.ID, "", "", 200)
	assertNoResponseValidators(t, raw)
	state, err := parseSnapshot(raw, b.ID)
	if err != nil || state.Board.Revision != 1 {
		t.Fatal("native revisions failed round trip", err)
	}
	stored, err := app.store.durable(b.ID)
	if err != nil || stored.Policy.Revision != 1 {
		t.Fatal("durable revisions failed validation", err)
	}
	// A full multi-value If-Match still uses strong matching at the request boundary.
	raw, headers = editor.call("PATCH", "/boards/"+b.ID, `{"name":"Next revision"}`, "W/"+b.entityTag()+`, "unrelated", `+b.entityTag(), 200)
	assertNoResponseValidators(t, raw)
	b = turnOneDecode[board](t, raw)
	if b.Revision != 2 || headers.Get("ETag") != etag(b.ID, 2) {
		t.Fatal("revision did not advance exactly once")
	}
	editor.call("PATCH", "/boards/"+b.ID, `{"name":"Stale"}`, etag(b.ID, 1), 412)
	raw, headers = editor.call("POST", "/lanes", fmt.Sprintf(`{"board_id":%q,"board_etag":%q,"label":"Extra","connect_for":[%q]}`, b.ID, b.entityTag(), humanID), "", 201)
	assertNoResponseValidators(t, raw)
	raw, policyHeaders := editor.call("GET", "/policies/"+p.ID, "", "", 200)
	assertNoResponseValidators(t, raw)
	updatedPolicy := turnOneDecode[policy](t, raw)
	if updatedPolicy.Revision != 3 || policyHeaders.Get("ETag") != etag(p.ID, 3) || headers.Get("X-Board-ETag") != etag(b.ID, 3) {
		t.Fatal("policy and board did not share their mutation revision")
	}
	raw, _ = editor.call("GET", "/snapshots/"+b.ID, "", "", 200)
	assertNoResponseValidators(t, raw)
	if _, err := parseSnapshot(raw, b.ID); err != nil {
		t.Fatal("updated native revisions did not validate", err)
	}

}

func TestNativeEntityRevisionValidation(t *testing.T) {
	fixture := nativeVerificationFixture()
	raw := marshalVerificationFixture(t, fixture)
	for _, resource := range []string{"board", "users", "priorities", "policy"} {
		for _, bad := range []string{"0", "2", "9007199254740992", "-1", "1.5", `"1"`, "null", "true", "1e0", "missing", "etag"} {
			t.Run(resource+"/"+bad, func(t *testing.T) {
				var document map[string]json.RawMessage
				if err := json.Unmarshal(raw, &document); err != nil {
					t.Fatal(err)
				}
				var entities []json.RawMessage
				isCollection := resource == "users" || resource == "priorities"
				if isCollection {
					if err := json.Unmarshal(document[resource], &entities); err != nil {
						t.Fatal(err)
					}
				} else {
					entities = []json.RawMessage{document[resource]}
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(entities[0], &fields); err != nil {
					t.Fatal(err)
				}
				switch bad {
				case "missing":
					delete(fields, "revision")
				case "etag":
					fields["etag"] = json.RawMessage(fmt.Sprintf("%q", etag(fixture.Board.ID, 1)))
				default:
					fields["revision"] = json.RawMessage(bad)
				}
				entities[0], _ = json.Marshal(fields)
				if isCollection {
					document[resource], _ = json.Marshal(entities)
				} else {
					document[resource] = entities[0]
				}
				damaged, _ := json.Marshal(document)
				if _, err := parseSnapshot(damaged, fixture.Board.ID); !errors.Is(err, errInvalidSnapshot) {
					t.Fatalf("invalid revision accepted: %v", err)
				}
			})
		}
	}
}

func TestNumericStoreRevisionSerializesConcurrentWriters(t *testing.T) {
	root := t.TempDir()
	store, initial := m01CreateBoard(t, root)
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		workers.Go(func() { _, err := store.update(initial.Board.ID, initial.Board.Revision, m01Rename); results <- err })
	}
	workers.Wait()
	close(results)
	committed, stale := 0, 0
	for err := range results {
		switch {
		case err == nil:
			committed++
		case errors.Is(err, errStale):
			stale++
		default:
			t.Fatal(err)
		}
	}
	if committed != 1 || stale != 1 {
		t.Fatalf("commits=%d stale=%d", committed, stale)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	_, principals := m01Fixture()
	reopened, err := openStore(root, principals...)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	state, ok := reopened.accepted(initial.Board.ID)
	if !ok || state.Board.Revision != 2 || len(state.Activity) != 2 {
		t.Fatal("serialized numeric revision did not survive restart")
	}
}
