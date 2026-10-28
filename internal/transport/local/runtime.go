// Package local composes the single-board HTTP product boundary. The only
// listener is numeric IPv4 loopback; authority remains in G01/S02 snapshots.
package local

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/localswim/localswim/internal/contract"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/transport/attachments"
	"github.com/localswim/localswim/internal/transport/boardapi"
	"github.com/localswim/localswim/internal/transport/exportapi"
)

var errLocal = errors.New("local service unavailable; reconnect using the selected data directory")

type Options struct {
	DataDir string
	Port    int
	Assets  http.Handler
}
type ClientInfo struct {
	InstallationID, Origin, BoardID, Incarnation, ActorID, GrantID, Epoch string
	WorkspaceID                                                           string
	Port                                                                  int
	Token                                                                 T.SecretToken
}

// SessionPath is derived solely from validated rendezvous mode and BoardID.
// Metadata never supplies an arbitrary URL or route.
func (c ClientInfo) SessionPath() string {
	if c.WorkspaceID != "" {
		return "/api/v1/boards/" + c.BoardID + "/session"
	}
	return "/api/v1/session"
}

type BoardIdentity struct{ InstallationID, BoardID, Incarnation string }
type EngineOptions struct {
	DataDir          string
	Port             int
	Origin           string
	WorkspaceID      string
	ExpectedIdentity *BoardIdentity
}

func (ClientInfo) String() string               { return "local connection [redacted]" }
func (c ClientInfo) GoString() string           { return c.String() }
func (ClientInfo) MarshalJSON() ([]byte, error) { return nil, errLocal }

type identity struct {
	Version           int    `json:"version"`
	RestoredRequestID string `json:"restoredRequestId,omitempty"`
	InstallationID    string `json:"installationId"`
	BoardID           string `json:"boardId"`
	Incarnation       string `json:"incarnation"`
	HumanID           string `json:"humanId"`
	BotID             string `json:"botId"`
	HumanGrant        string `json:"humanGrant"`
	BotGrant          string `json:"botGrant"`
	Port              int    `json:"port"`
}
type metadata struct {
	Identity identity `json:"identity"`
	Epoch    string   `json:"epoch"`
	Origin   string   `json:"origin"`
	BotToken string   `json:"botToken"`
}
type workspaceMetadata struct {
	MetadataVersion int                   `json:"metadataVersion"`
	Identity        identity              `json:"identity"`
	Epoch           string                `json:"epoch"`
	Origin          string                `json:"origin"`
	BotToken        string                `json:"botToken"`
	Host            workspaceMetadataHost `json:"host"`
}
type workspaceMetadataHost struct {
	Mode        string `json:"mode"`
	WorkspaceID string `json:"workspaceId"`
}

func (m metadata) String() string            { return "local metadata [redacted]" }
func (m metadata) GoString() string          { return m.String() }
func (workspaceMetadata) String() string     { return "workspace member metadata [redacted]" }
func (m workspaceMetadata) GoString() string { return m.String() }
func uuid() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic("local random source unavailable")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func token() (T.SecretToken, error) {
	var b [32]byte
	_, e := rand.Read(b[:])
	if e != nil {
		return "", errLocal
	}
	return T.SecretToken(base64.RawURLEncoding.EncodeToString(b[:])), nil
}
func decode(b []byte, v any) error {
	if _, e := contract.DecodeJSON(b); e != nil {
		return errLocal
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return errLocal
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errLocal
	}
	return nil
}
func (i identity) valid() bool {
	if (i.Version != 1 && i.Version != 2) || (i.Version == 1 && i.RestoredRequestID != "") || (i.Version == 2 && !schema.ValidUUID(i.RestoredRequestID)) || i.Port < 1024 || i.Port > 65535 || !uuidV4(i.InstallationID) {
		return false
	}
	ids := []string{i.InstallationID, i.BoardID, i.Incarnation, i.HumanID, i.BotID, i.HumanGrant, i.BotGrant}
	seen := map[string]bool{}
	for _, id := range ids {
		if !schema.ValidUUID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func uuidV4(v string) bool {
	return schema.ValidUUID(v) && v[14] == '4' && strings.ContainsRune("89ab", rune(v[19]))
}
func origin(port int) string { return "http://127.0.0.1:" + strconv.Itoa(port) }

// ReadClient reads only bounded account-private bot metadata. It is not a health
// check: CLI must compare the live session/context before dispatching a mutation.
func ReadClient(dataDir string) (ClientInfo, error) {
	var out ClientInfo
	release, e := pinRoot(dataDir, false)
	if e != nil {
		return out, e
	}
	defer release()
	b, e := readPrivate(filepath.Join(dataDir, "identity.json"))
	if e != nil {
		return out, e
	}
	var id identity
	if decode(b, &id) != nil || !id.valid() {
		return out, errLocal
	}
	b, e = readPrivateBounded(filepath.Join(dataDir, "service.json"), 64<<10)
	if e != nil {
		return out, e
	}
	m, workspaceID, e := decodeMetadata(b)
	if e != nil || m.Identity != id {
		return out, errLocal
	}
	return ClientInfo{InstallationID: id.InstallationID, Origin: m.Origin, BoardID: id.BoardID, Incarnation: id.Incarnation, ActorID: id.BotID, GrantID: id.BotGrant, Epoch: m.Epoch, Port: id.Port, Token: T.SecretToken(m.BotToken), WorkspaceID: workspaceID}, nil
}

// decodeMetadata accepts two exact tagged alternatives, including required keys.
func decodeMetadata(b []byte) (metadata, string, error) {
	bad := func() (metadata, string, error) { return metadata{}, "", errLocal }
	v, e := contract.DecodeJSON(b)
	if e != nil {
		return bad()
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return bad()
	}
	var m metadata
	workspaceID := ""
	if _, tagged := obj["metadataVersion"]; tagged {
		var w workspaceMetadata
		if len(obj) != 6 || decode(b, &w) != nil || w.MetadataVersion != 2 || w.Host.Mode != "workspace" || !uuidV4(w.Host.WorkspaceID) || !canonicalOrigin(w.Origin) {
			return bad()
		}
		m = metadata{w.Identity, w.Epoch, w.Origin, w.BotToken}
		workspaceID = w.Host.WorkspaceID
	} else {
		if len(obj) != 4 || decode(b, &m) != nil || m.Origin != origin(m.Identity.Port) {
			return bad()
		}
	}
	if !m.Identity.valid() || !uuidV4(m.Epoch) || !validToken(m.BotToken) {
		return bad()
	}
	return m, workspaceID, nil
}

func canonicalOrigin(s string) bool {
	const prefix = "http://127.0.0.1:"
	if !strings.HasPrefix(s, prefix) {
		return false
	}
	p, e := strconv.Atoi(strings.TrimPrefix(s, prefix))
	return e == nil && p >= 1024 && p <= 65535 && s == origin(p)
}
func validToken(s string) bool {
	b, e := base64.RawURLEncoding.Strict().DecodeString(s)
	return e == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == s
}

type Runtime struct {
	id                           identity
	epoch, root                  string
	effectiveOrigin, workspaceID string
	store                        *S.Store
	auth                         *authority
	server                       *http.Server
	listener                     net.Listener
	done                         chan struct{}
	closeMu                      sync.Mutex
	closed                       bool
	reqMu                        sync.Mutex
	closing                      bool
	requests                     sync.WaitGroup
	closeErr                     error
	storeCloseErr                error
	release                      func()
	assets                       http.Handler
	board, attachment, export    http.Handler
}

func (r *Runtime) Origin() string { return r.effectiveOrigin }
func (r *Runtime) Identity() BoardIdentity {
	return BoardIdentity{r.id.InstallationID, r.id.BoardID, r.id.Incarnation}
}
func (r *Runtime) Done() <-chan struct{} { return r.done }
func (r *Runtime) Close() error {
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	if r.closed {
		return r.closeErr
	}
	// Close admission before Shutdown: a net/http handler may be scheduled
	// after Shutdown/Close returns. Its entry must not Add after Wait or
	// touch authority/storage after their lifetime has been released.
	r.reqMu.Lock()
	r.closing = true
	r.reqMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var e error
	if r.server != nil {
		e = r.server.Shutdown(ctx)
		if e != nil {
			_ = r.server.Close()
		}
	}
	r.requests.Wait()
	r.auth.close()
	// Metadata removal happens while the exclusive root lock is still held.
	rm := r.removeMetadata()
	if rm != nil {
		return errors.Join(e, rm)
	}
	if r.storeCloseErr != nil {
		return r.storeCloseErr
	}
	if se := r.store.Close(); se != nil {
		r.storeCloseErr = errors.Join(e, se)
		return r.storeCloseErr
	}
	r.closeErr = e
	r.release()
	r.closed = true
	close(r.done)
	return r.closeErr
}

func (r *Runtime) removeMetadata() error {
	p := filepath.Join(r.root, "service.json")
	b, e := readPrivateBounded(p, 64<<10)
	if e != nil {
		if _, se := os.Lstat(p); errors.Is(se, os.ErrNotExist) {
			return nil
		}
		return errLocal
	}
	m, workspaceID, e := decodeMetadata(b)
	if e != nil {
		return e
	}
	if m.Epoch != r.epoch || m.Identity != r.id || m.Origin != r.Origin() || workspaceID != r.workspaceID {
		return nil
	}
	return os.Remove(p)
}

// validateIdentity rechecks durable identity under the pinned root at selected
// dispatch. Replacement never remaps a registered member or reuses authority.
func (r *Runtime) validateIdentity() bool {
	b, e := readPrivate(filepath.Join(r.root, "identity.json"))
	var id identity
	return e == nil && decode(b, &id) == nil && id == r.id
}

func Start(ctx context.Context, o Options) (*Runtime, error) {
	r, e := openEngine(ctx, EngineOptions{DataDir: o.DataDir, Port: o.Port}, o.Assets)
	if e != nil {
		return nil, e
	}
	r.listener, e = net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(r.id.Port))
	if e != nil {
		return nil, errors.Join(errLocal, r.Close())
	}
	r.server = newServer(r)
	go func() { _ = r.server.Serve(r.listener); _ = r.Close() }()
	r.watch(ctx)
	return r, nil
}

// OpenEngine acquires one independent board without opening a listener. The
// caller supplies the canonical effective host context; persistent port remains
// the standalone default and is never rewritten for workspace membership.
func OpenEngine(ctx context.Context, o EngineOptions) (*Runtime, error) {
	if !uuidV4(o.WorkspaceID) || !canonicalOrigin(o.Origin) {
		return nil, errLocal
	}
	r, e := openEngine(ctx, o, nil)
	if e == nil {
		r.watch(ctx)
	}
	return r, e
}

func newServer(handler http.Handler) *http.Server {
	return &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
}

func openEngine(ctx context.Context, o EngineOptions, assets http.Handler) (*Runtime, error) {
	if ctx == nil || ctx.Err() != nil || !safeDir(o.DataDir) {
		return nil, errLocal
	}
	if o.Port != 0 && (o.Port < 1024 || o.Port > 65535) {
		return nil, errLocal
	}
	// Registered members must already exist. A missing expected root must not
	// create even a new exclusion file, much less initialize replacement bytes.
	release, e := pinRoot(o.DataDir, o.ExpectedIdentity == nil)
	if e != nil {
		return nil, e
	}
	unlock, e := lockRoot(o.DataDir)
	if e != nil {
		release()
		return nil, e
	}
	cleanup := func() { unlock(); release() }
	ok := false
	defer func() {
		if !ok {
			cleanup()
		}
	}()
	var id identity
	b, e := readPrivate(filepath.Join(o.DataDir, "identity.json"))
	fresh := false
	if e != nil {
		if o.ExpectedIdentity != nil {
			return nil, errLocal
		}
		// Only a truly uninitialized root may initialize. Failed/partial stores and
		// unknown metadata are preserved for explicit investigation, never adopted.
		entries, er := os.ReadDir(o.DataDir)
		if er != nil {
			return nil, errLocal
		}
		for _, entry := range entries {
			if entry.Name() != "local.lock" {
				return nil, errLocal
			}
		}
		if o.Port == 0 {
			o.Port = 8792
		}
		id = identity{Version: 1, InstallationID: uuid(), BoardID: uuid(), Incarnation: uuid(), HumanID: uuid(), BotID: uuid(), HumanGrant: uuid(), BotGrant: uuid(), Port: o.Port}
		fresh = true
		if privateDir(filepath.Join(o.DataDir, "board")) != nil {
			return nil, errLocal
		}
	} else if decode(b, &id) != nil || !id.valid() || o.Port != 0 && id.Port != o.Port {
		return nil, errLocal
	}
	if !fresh && o.WorkspaceID != "" && o.ExpectedIdentity == nil {
		return nil, errLocal
	}
	if o.ExpectedIdentity != nil && *o.ExpectedIdentity != (BoardIdentity{id.InstallationID, id.BoardID, id.Incarnation}) {
		return nil, errLocal
	}
	boardDir := filepath.Join(o.DataDir, "board")
	if id.Version == 2 {
		if _, e := restoreEvidence(o.DataDir, id, id.RestoredRequestID); e != nil {
			return nil, errLocal
		}
		boardDir = filepath.Join(o.DataDir, "data")
	}
	var store *S.Store
	sid := schema.Identity{BoardID: id.BoardID, Incarnation: id.Incarnation}
	if fresh {
		store, e = S.Create(ctx, boardDir, sid)
	} else {
		store, e = S.Existing(ctx, boardDir, sid)
	}
	if e != nil {
		return nil, e
	}
	storeOK := false
	defer func() {
		if !storeOK {
			_ = store.Close()
		}
	}()
	if fresh {
		if e = initialize(ctx, store, id); e != nil {
			return nil, e
		}
		b, e = json.Marshal(id)
		if e != nil {
			return nil, errLocal
		}
		if e = publish(filepath.Join(o.DataDir, "identity.json"), b, false); e != nil {
			return nil, e
		}
	}
	// CLI evidence is separate from S01's strict board directory inventory.
	if _, e = os.Lstat(filepath.Join(o.DataDir, "cli-evidence")); errors.Is(e, os.ErrNotExist) {
		if privateDir(filepath.Join(o.DataDir, "cli-evidence")) != nil {
			return nil, errLocal
		}
	}
	bot, e := token()
	if e != nil {
		return nil, e
	}
	effectiveOrigin := o.Origin
	if o.WorkspaceID == "" {
		effectiveOrigin = origin(id.Port)
	}
	r := &Runtime{id: id, epoch: uuid(), root: o.DataDir, store: store, done: make(chan struct{}), release: cleanup, assets: assets, effectiveOrigin: effectiveOrigin, workspaceID: o.WorkspaceID}
	r.auth = newAuthority(store, id, r.Origin(), bot)
	r.board, e = boardapi.NewAuthenticated(store, r.auth, r)
	if e != nil {
		return nil, e
	}
	r.attachment, e = attachments.NewAuthenticated(store, r.auth, r)
	if e != nil {
		return nil, e
	}
	r.export, e = exportapi.NewAuthenticated(store, r.auth, r)
	if e != nil {
		return nil, e
	}
	var rendezvous any = metadata{id, r.epoch, r.Origin(), string(bot)}
	if o.WorkspaceID != "" {
		rendezvous = workspaceMetadata{2, id, r.epoch, r.Origin(), string(bot), workspaceMetadataHost{"workspace", o.WorkspaceID}}
	}
	b, e = json.Marshal(rendezvous)
	if e != nil || publish(filepath.Join(o.DataDir, "service.json"), b, true) != nil {
		return nil, errLocal
	}
	ok = true
	storeOK = true
	return r, nil
}

func (r *Runtime) watch(ctx context.Context) {
	go func() {
		select {
		case <-ctx.Done():
			_ = r.Close()
		case <-r.done:
		}
	}()
}

func initialize(ctx context.Context, store *S.Store, id identity) (err error) {
	l, e := store.Writer(ctx)
	if e != nil {
		return e
	}
	defer func() { err = errors.Join(err, l.Close()) }()
	return l.Do(ctx, func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx, "BEGIN IMMEDIATE"); e != nil {
			return e
		}
		for _, v := range []struct{ actor, grant, kind, label string }{{id.HumanID, id.HumanGrant, "human", "Human"}, {id.BotID, id.BotGrant, "automation", "Bot"}} {
			if _, e := c.ExecContext(ctx, "INSERT INTO actor VALUES(?,?,?,?,?,?)", id.Incarnation, v.actor, v.kind, v.label, id.BoardID, id.Incarnation); e != nil {
				return e
			}
			if _, e := c.ExecContext(ctx, "INSERT INTO grant_fact VALUES(?,?)", id.Incarnation, v.grant); e != nil {
				return e
			}
			if _, e := c.ExecContext(ctx, "INSERT INTO grant_record VALUES(?,?,?,1)", id.Incarnation, v.grant, v.actor); e != nil {
				return e
			}
			rights := []string{"attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"}
			if v.kind == "human" {
				rights = append(rights, "approve")
			}
			for _, right := range rights {
				if _, e := c.ExecContext(ctx, "INSERT INTO grant_right VALUES(?,?,?)", id.Incarnation, v.grant, right); e != nil {
					return e
				}
			}
		}
		_, e := c.ExecContext(ctx, "COMMIT")
		return e
	})
}
