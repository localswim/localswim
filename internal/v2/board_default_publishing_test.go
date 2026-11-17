package v2

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// A board created while the workspace publishes starts publishing on its own,
// through an audited enable that verification accepts, and reaches the remote
// without a manual board publishing enable.
func TestNewBoardInheritsWorkspacePublishing(t *testing.T) {
	shortenPublicationTiming(t, publicationTiming{quiet: 200 * time.Millisecond, maxDelay: time.Second})
	fixture := m34PublicationFixture(t)
	editor := fixture.editor

	raw, _ := editor.call("POST", "/boards", `{"name":"Before workspace publishing"}`, "", 201)
	if created := turnOneDecode[board](t, raw); created.Revision != 1 || created.Publishing.Enabled {
		t.Fatalf("board created while the workspace was off: revision %d, publishing %t", created.Revision, created.Publishing.Enabled)
	}

	_, headers := editor.call("GET", "/workspace", "", "", 200)
	editor.call("PATCH", "/workspace", `{"publishing":{"enabled":true,"repository":"origin"}}`, headers.Get("ETag"), 200)
	raw, _ = editor.call("POST", "/boards", `{"name":"Backed up from birth"}`, "", 201)
	created := turnOneDecode[board](t, raw)
	if created.Revision != 2 || !created.Publishing.Enabled || !created.Publishing.Pending ||
		snapshotString(created.Publishing.Repository) != "origin" {
		t.Fatalf("new board did not inherit publishing: revision %d, %+v", created.Revision, created.Publishing)
	}

	durable, err := fixture.app.store.durable(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	for _, event := range durable.Activity {
		if event.Revision == 2 {
			if event.ActorID != humanID || event.Operation != "patch" || event.ResourceType != "board" {
				t.Fatalf("enable audit is not the creator's board patch: %+v", event)
			}
			fields = append(fields, snapshotString(event.Field)+"="+snapshotString(event.After))
		}
	}
	if strings.Join(fields, ",") != "publishing.enabled=true,publishing.repository=origin" {
		t.Fatalf("revision 2 audit: %v", fields)
	}
	raw, _ = editor.call("GET", "/verifications/"+created.ID, "", "", 200)
	if report := turnOneDecode[verification](t, raw); !report.Valid {
		t.Fatalf("default-on board failed verification: %+v", report.Issues)
	}
	if report, err := VerifyOffline(fixture.app.store.root, created.ID); err != nil || !report.Valid {
		t.Fatalf("offline verification: %+v, %v", report.Issues, err)
	}

	path := fixture.prefix + "/boards/" + created.ID + ".json"
	deadline := time.Now().Add(30 * time.Second)
	for {
		probe := exec.Command("git", "cat-file", "-e", "refs/heads/main:"+path)
		probe.Dir = fixture.remote
		if probe.Run() == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the new board never reached the remote")
		}
		time.Sleep(100 * time.Millisecond)
	}
	raw, _ = editor.call("GET", "/boards/"+created.ID, "", "", 200)
	current := turnOneDecode[board](t, raw)
	editor.call("PATCH", "/boards/"+created.ID, `{"publishing":{"enabled":false}}`, current.entityTag(), 200)
}

// The creator needs read and publish, as for a publishing patch, and every
// outcome MUST still pass snapshot verification.
func TestInheritPublishingNeedsPreferenceAndCapability(t *testing.T) {
	now := canonicalTime(time.Now())
	id := newID()
	principals := []user{
		{ID: humanID, Revision: 1, CreatedAt: now, Kind: "human", Label: "Human"},
		{ID: botID, Revision: 1, CreatedAt: now, Kind: "bot", Label: "Bot"},
	}
	origin := "origin"
	for _, check := range []struct {
		name       string
		preference publishing
		without    string
		enabled    bool
	}{
		{"workspace off", publishing{}, "", false},
		{"creator may publish", publishing{Enabled: true}, "", true},
		{"creator may publish to origin", publishing{Enabled: true, Repository: &origin}, "", true},
		{"creator may not publish", publishing{Enabled: true}, "publish", false},
		{"creator may publish but not read", publishing{Enabled: true, Repository: &origin}, "read", false},
	} {
		s := freshSnapshot(id, "Board", defaultWorkflow(id, now, principals), humanID, now)
		for i, grant := range s.Policy.ResourceGrants {
			if grant.UserID == humanID {
				s.Policy.ResourceGrants[i].Capabilities = slices.DeleteFunc(slices.Clone(grant.Capabilities),
					func(capability string) bool { return capability == check.without })
			}
		}
		events := len(s.Activity)
		inheritPublishing(&s, check.preference, humanID, now)
		if s.Board.Publishing.Enabled != check.enabled || (s.Board.Revision == 2) != check.enabled ||
			(len(s.Activity) > events) != check.enabled {
			t.Errorf("%s: enabled=%t revision=%d events=%d", check.name, s.Board.Publishing.Enabled, s.Board.Revision, len(s.Activity)-events)
		}
		if check.enabled && !sameText(s.Board.Publishing.Repository, check.preference.Repository) {
			t.Errorf("%s: repository %v was not inherited from the preference", check.name, s.Board.Publishing.Repository)
		}
		if err := validateSnapshot(s, id); err != nil {
			t.Errorf("%s: verification refused the new board: %v", check.name, err)
		}
	}
}
