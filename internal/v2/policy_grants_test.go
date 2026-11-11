package v2

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestPolicyGrantsHTTP(t *testing.T) {
	root := t.TempDir()
	human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(root, human, bot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewUnstartedServer(nil)
	address := server.Listener.Addr().(*net.TCPAddr)
	server.Config.Handler = app.Handler(address)
	server.Start()
	t.Cleanup(server.Close)
	c := turnOneClient{t, server.URL, bot, server.Client()}

	raw, _ := c.call("POST", "/boards", `{"name":"Policy fixture"}`, "", 201)
	created := turnOneDecode[board](t, raw)
	path := "/policies/" + created.PolicyID
	raw, header := c.call("GET", path, "", "", 200)
	old := turnOneDecode[policy](t, raw)
	if header.Get("ETag") != old.entityTag() {
		t.Fatal("initial policy tag is missing")
	}
	state, ok := app.store.accepted(created.ID)
	if !ok {
		t.Fatal("board was not accepted")
	}
	create := createGrant{UserID: botID, LaneID: state.Lanes[2].ID}
	move := moveGrant{UserID: botID, FromLaneID: state.Lanes[0].ID, ToLaneID: state.Lanes[2].ID}
	if slices.Contains(old.CreateGrants, create) || slices.Contains(old.MoveGrants, move) {
		t.Fatal("fixture already grants the test permissions")
	}
	expanded := old
	expanded.CreateGrants = append(slices.Clone(old.CreateGrants), create)
	expanded.MoveGrants = append(slices.Clone(old.MoveGrants), move)
	good := policyPutBody(t, expanded, created.entityTag())
	durablePath := app.store.snapshotPath(created.ID)
	before, err := os.ReadFile(durablePath)
	if err != nil {
		t.Fatal(err)
	}

	unknown := expanded
	unknown.MoveGrants = append(slices.Clone(expanded.MoveGrants), moveGrant{
		UserID: botID, FromLaneID: state.Lanes[0].ID, ToLaneID: newID(),
	})
	duplicate := expanded
	duplicate.CreateGrants = append(slices.Clone(expanded.CreateGrants), createGrant{
		UserID: strings.ToUpper(create.UserID), LaneID: strings.ToUpper(create.LaneID),
	})
	removed := expanded
	removed.MoveGrants = slices.Clone(expanded.MoveGrants[1:])
	defaultChanged := expanded
	defaultChanged.DefaultPriorityID = state.Priorities[0].ID
	resourceChanged := expanded
	resourceChanged.ResourceGrants = slices.Clone(expanded.ResourceGrants)
	resourceChanged.ResourceGrants[0].Capabilities = resourceChanged.ResourceGrants[0].Capabilities[1:]
	stale := etag(created.ID, created.Revision+1)
	cases := []struct {
		name, body, policyTag string
		status                int
	}{
		{"missing policy tag", good, "", 428},
		{"missing board tag", policyAuditValue(expanded), old.entityTag(), 400},
		{"malformed board tag", policyPutBody(t, expanded, "W/"+created.entityTag()), old.entityTag(), 400},
		{"stale board tag", policyPutBody(t, expanded, stale), old.entityTag(), 412},
		{"stale policy tag", good, etag(old.ID, created.Revision+1), 412},
		{"weak policy tag", good, "W/" + old.entityTag(), 412},
		{"wildcard policy tag", good, "*", 412},
		{"unknown lane with valid grants", policyPutBody(t, unknown, created.entityTag()), old.entityTag(), 400},
		{"duplicate normalized grants", policyPutBody(t, duplicate, created.entityTag()), old.entityTag(), 400},
		{"grant removal", policyPutBody(t, removed, created.entityTag()), old.entityTag(), 409},
		{"default priority change", policyPutBody(t, defaultChanged, created.entityTag()), old.entityTag(), 409},
		{"resource permission change", policyPutBody(t, resourceChanged, created.entityTag()), old.entityTag(), 409},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := c
			client.t = t
			raw, _ := client.call("PUT", path, tc.body, tc.policyTag, tc.status)
			if problem := turnOneDecode[problemBody](t, raw); problem.Outcome != "not_applied" {
				t.Fatalf("known refusal outcome = %q", problem.Outcome)
			}
		})
	}

	for header, value := range map[string]string{"Origin": server.URL, "Sec-Fetch-Site": "same-origin"} {
		req := httptest.NewRequest("PUT", server.URL+Prefix+path, strings.NewReader(good))
		req.URL.Scheme, req.URL.Host = "", ""
		req.Header.Set("Authorization", "Bearer "+human)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", old.entityTag())
		req.Header.Set(header, value)
		recorder := httptest.NewRecorder()
		app.Handler(address).ServeHTTP(recorder, req)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("browser request with %s status = %d, want 403", header, recorder.Code)
		}
	}
	preflight := httptest.NewRequest("OPTIONS", server.URL+Prefix+path, nil)
	preflight.URL.Scheme, preflight.URL.Host = "", ""
	preflight.Header.Set("Origin", server.URL)
	preflight.Header.Set("Access-Control-Request-Method", "PUT")
	recorder := httptest.NewRecorder()
	app.Handler(address).ServeHTTP(recorder, preflight)
	if recorder.Code != http.StatusForbidden || strings.Contains(recorder.Header().Get("Access-Control-Allow-Methods"), "PUT") {
		t.Fatal("policy preflight granted browser PUT")
	}

	afterRefusals, err := os.ReadFile(durablePath)
	if err != nil || !bytes.Equal(before, afterRefusals) {
		t.Fatalf("a refused update changed the durable snapshot (read error: %v)", err)
	}

	raw, header = c.call("PUT", path, good, old.entityTag(), 200)
	replaced := turnOneDecode[policy](t, raw)
	boardTag := header.Get("X-Board-ETag")
	if header.Get("ETag") != "" || boardTag != stale || replaced.entityTag() == old.entityTag() ||
		replaced.ID != old.ID || replaced.BoardID != old.BoardID || replaced.CreatedAt != old.CreatedAt ||
		!slices.Equal(replaced.CreateGrants, expanded.CreateGrants) || !slices.Equal(replaced.MoveGrants, expanded.MoveGrants) {
		t.Fatal("the successful replacement did not return the committed policy and board version")
	}

	got, header := c.call("GET", path, "", "", 200)
	if !bytes.Equal(got, raw) || header.Get("ETag") != replaced.entityTag() {
		t.Fatal("policy GET did not read back the replacement and its tag")
	}
	durable, err := readSnapshot(durablePath, created.ID)
	if err != nil || durable.Board.entityTag() != boardTag || len(durable.Activity) != 2 {
		t.Fatalf("the replacement did not persist as one audited commit: %v", err)
	}
	event := durable.Activity[1]
	_, valid := checkedPolicyAuditEvent(durable, event, "")
	if event.ActorID != botID || event.ResourceID != old.ID || !valid {
		t.Fatal("invalid policy grant audit")
	}
	durable.Activity[1].Before = durable.Activity[1].After
	if err := validateSnapshot(durable, created.ID); err == nil {
		t.Fatal("invalid policy audit admitted")
	}

	raw, header = c.call("PUT", path, policyPutBody(t, replaced, boardTag), replaced.entityTag(), 200)
	if header.Get("X-Board-ETag") != boardTag || turnOneDecode[policy](t, raw).entityTag() != replaced.entityTag() {
		t.Fatal("reapplying the current policy advanced a revision")
	}
	c.call("PUT", path, good, old.entityTag(), 412)
}

func policyPutBody(t *testing.T, p policy, boardTag string) string {
	t.Helper()
	fields, err := decodeObject([]byte(policyAuditValue(p)))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(boardTag)
	if err != nil {
		t.Fatal(err)
	}
	fields["board_etag"] = raw
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
