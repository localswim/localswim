//go:build windows && amd64

package local

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/localswim/localswim/internal/board/grants"
)

func configuredReadEngine(t *testing.T) (*Runtime, string) {
	t.Helper()
	root := filepath.Join(testRoot(t), "configured-read")
	r, err := OpenEngine(context.Background(), EngineOptions{DataDir: root, WorkspaceID: uuid(), Origin: "http://127.0.0.1:18419"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r, root
}

// These are deliberate synthetic authority faults and restorations, not an
// administrative API or a positive customer fixture. Every change uses the
// already-owned engine Store writer; no second SQL writer is opened.
func configuredGrantSQL(t *testing.T, r *Runtime, query string, args ...any) {
	t.Helper()
	lease, err := r.store.Writer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = lease.Do(context.Background(), func(c *sql.Conn) error {
		result, err := c.ExecContext(context.Background(), query, args...)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err == nil && n != 1 {
			t.Errorf("authority fixture affected %d rows, want one", n)
		}
		return err
	})
	closeErr := lease.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("authority fixture failed: execute=%v close=%v", err, closeErr)
	}
}

func configuredCredentials(r *Runtime) map[[32]byte]grants.Credential {
	r.auth.mu.Lock()
	defer r.auth.mu.Unlock()
	out := make(map[[32]byte]grants.Credential, len(r.auth.credentials))
	for key, value := range r.auth.credentials {
		out[key] = value
	}
	return out
}

func configuredReadStates(t *testing.T, r *Runtime, browser, cli bool) {
	t.Helper()
	for channel, want := range map[string]bool{"browser": browser, "cli": cli} {
		if got := r.ConfiguredReadAllowed(context.Background(), channel); got != want {
			t.Errorf("configured %s read=%t, want %t", channel, got, want)
		}
	}
}

func configuredMetadataHashes(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	for _, name := range []string{"identity.json", "service.json"} {
		data, err := readPrivate(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		out[name] = sha256.Sum256(data)
	}
	return out
}

func TestWorkspaceConfiguredReadCurrentGrantSnapshot(t *testing.T) {
	r, root := configuredReadEngine(t)
	credentials := configuredCredentials(r)
	before := configuredMetadataHashes(t, root)
	configuredReadStates(t, r, true, true)
	for _, selected := range []struct {
		name, grant, actor, otherActor string
		browser, cli                   bool
	}{
		{"human", r.id.HumanGrant, r.id.HumanID, r.id.BotID, false, true},
		{"automation", r.id.BotGrant, r.id.BotID, r.id.HumanID, true, false},
	} {
		t.Run(selected.name, func(t *testing.T) {
			// Removing the read right leaves the grant enabled and other rights
			// intact: neither a configured identity nor another right is enough.
			configuredGrantSQL(t, r, "DELETE FROM grant_right WHERE incarnation=? AND grant_id=? AND right_name='read'", r.id.Incarnation, selected.grant)
			configuredReadStates(t, r, selected.browser, selected.cli)
			configuredGrantSQL(t, r, "INSERT INTO grant_right(incarnation,grant_id,right_name) VALUES(?,?,'read')", r.id.Incarnation, selected.grant)
			configuredReadStates(t, r, true, true)

			configuredGrantSQL(t, r, "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, selected.grant)
			configuredReadStates(t, r, selected.browser, selected.cli)
			configuredGrantSQL(t, r, "UPDATE grant_record SET enabled=1 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, selected.grant)
			configuredReadStates(t, r, true, true)

			// A valid, enabled grant belonging to the other durable actor must
			// not substitute for this channel's configured actor/grant pair.
			configuredGrantSQL(t, r, "UPDATE grant_record SET actor_id=? WHERE incarnation=? AND grant_id=?", selected.otherActor, r.id.Incarnation, selected.grant)
			configuredReadStates(t, r, selected.browser, selected.cli)
			configuredGrantSQL(t, r, "UPDATE grant_record SET actor_id=? WHERE incarnation=? AND grant_id=?", selected.actor, r.id.Incarnation, selected.grant)
			configuredReadStates(t, r, true, true)
		})
	}

	// Real WAL snapshots: an uncommitted disablement is invisible to a
	// concurrent reader, and the next predicate read sees its committed state.
	lease, err := r.store.Writer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = lease.Do(context.Background(), func(c *sql.Conn) error {
		if _, err := c.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		if _, err := c.ExecContext(context.Background(), "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, r.id.HumanGrant); err != nil {
			return err
		}
		configuredReadStates(t, r, true, true)
		_, err := c.ExecContext(context.Background(), "COMMIT")
		return err
	})
	closeErr := lease.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("snapshot fixture failed: execute=%v close=%v", err, closeErr)
	}
	configuredReadStates(t, r, false, true)
	configuredGrantSQL(t, r, "UPDATE grant_record SET enabled=1 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, r.id.HumanGrant)
	configuredReadStates(t, r, true, true)
	if !reflect.DeepEqual(credentials, configuredCredentials(r)) {
		t.Fatal("projection predicate changed board credential registry")
	}
	// SQL fixture writes can change the WAL/database, but the predicate must
	// not publish rendezvous credentials or modify private identity files.
	after := configuredMetadataHashes(t, root)
	for _, name := range []string{"identity.json", "service.json"} {
		if before[name] != after[name] {
			t.Fatal("predicate changed private member metadata")
		}
	}
}

func TestWorkspaceConfiguredReadAuthenticatedChannelConsumer(t *testing.T) {
	// This handler tests the real host's channel authentication and the local
	// predicate together. Registry list/resolve projections have separate tests
	// in internal/workspace; this is not a replacement for those integrations.
	var member atomic.Pointer[Runtime]
	h, a, _, root, _ := workspaceTest(t, http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if !member.Load().ConfiguredReadAllowed(q.Context(), WorkspaceChannel(q.Context())) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	member.Store(a)
	bootstrap := request(t, a, "POST", "/api/v1/workspace/bootstrap", []byte("{}"), "", nil, func(q *http.Request) {
		q.Header.Set("Origin", h.Origin())
		q.Header.Set("X-Localswim-Client", "browser")
	})
	if bootstrap.status != 200 || len(bootstrap.cookies) != 1 {
		t.Fatal("workspace browser bootstrap")
	}
	cookie := bootstrap.cookies[0]
	before := configuredCredentials(a)
	check := func(browser, cli int) {
		t.Helper()
		for _, test := range []struct {
			browser bool
			want    int
		}{{true, browser}, {false, cli}} {
			var out response
			if test.browser {
				out = request(t, a, "GET", "/api/v1/workspace/catalog", nil, "", cookie, nil)
			} else {
				out = request(t, a, "GET", "/api/v1/workspace/catalog", nil, h.AutomationToken(), nil, nil)
			}
			if out.status != test.want || len(out.b) != 0 || len(out.cookies) != 0 {
				t.Errorf("channel consumer status=%d want=%d, no data or credentials expected", out.status, test.want)
			}
		}
	}
	check(204, 204)
	configuredGrantSQL(t, a, "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", a.id.Incarnation, a.id.HumanGrant)
	check(503, 204)
	configuredGrantSQL(t, a, "UPDATE grant_record SET enabled=1 WHERE incarnation=? AND grant_id=?", a.id.Incarnation, a.id.HumanGrant)
	configuredGrantSQL(t, a, "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", a.id.Incarnation, a.id.BotGrant)
	check(204, 503)
	configuredGrantSQL(t, a, "UPDATE grant_record SET enabled=1 WHERE incarnation=? AND grant_id=?", a.id.Incarnation, a.id.BotGrant)
	check(204, 204)
	if !reflect.DeepEqual(before, configuredCredentials(a)) {
		t.Fatal("workspace projection minted board credentials")
	}
	h.mu.Lock()
	_, sameCookie := h.credentials[sha256.Sum256([]byte(cookie.Value))]
	count := len(h.credentials)
	h.mu.Unlock()
	if !sameCookie || count != 1 {
		t.Fatal("predicate minted or replaced a workspace credential")
	}
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	if out := request(t, a, "GET", "/api/v1/workspace/catalog", nil, info.Token, nil, nil); out.status != 401 {
		t.Fatal("board credential authenticated workspace predicate consumer")
	}
}

func TestWorkspaceConfiguredReadLifecycleDenials(t *testing.T) {
	r, root := configuredReadEngine(t)
	configuredReadStates(t, r, true, true)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, channel := range []string{"", "human", "automation", "native-owner", "Browser"} {
		if r.ConfiguredReadAllowed(context.Background(), channel) {
			t.Error("unrecognized channel accepted")
		}
	}
	for _, channel := range []string{"browser", "cli"} {
		if r.ConfiguredReadAllowed(nil, channel) || r.ConfiguredReadAllowed(canceled, channel) {
			t.Error("missing or canceled request accepted")
		}
	}
	path := filepath.Join(root, "identity.json")
	original, err := readPrivate(path)
	if err != nil {
		t.Fatal(err)
	}
	replaced := r.id
	replaced.Incarnation = uuid()
	data, err := json.Marshal(replaced)
	if err != nil {
		t.Fatal(err)
	}
	if err := publish(path, data, true); err != nil {
		t.Fatal(err)
	}
	configuredReadStates(t, r, false, false)
	if err := publish(path, original, true); err != nil {
		t.Fatal(err)
	}
	configuredReadStates(t, r, true, true)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	configuredReadStates(t, r, false, false)
}
