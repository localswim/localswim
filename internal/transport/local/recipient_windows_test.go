//go:build windows && amd64

package local

import (
	"bytes"
	"context"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"os"
	"path/filepath"
	"testing"
)

func TestRecipientNativeOrdinaryHTTP(t *testing.T) {
	if os.Getenv("L02_TEST_ROOT") == "" {
		t.Fatal("requires explicit private native fixture parent")
	}
	parent := testRoot(t)
	defer func() {
		if e := os.RemoveAll(parent); e != nil {
			t.Error(e)
		}
	}()
	root := filepath.Join(parent, "recipient")
	r, e := Start(context.Background(), Options{DataDir: root, Port: freePort(t)})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := r.Close(); e != nil {
			t.Error(e)
		}
	}()
	info, e := ReadClient(root)
	if e != nil {
		t.Fatal(e)
	}
	prefix := r.boardPath() + "recipient/"
	cookie := bootstrapTest(t, r)
	read := func() T.RecipientView {
		x := request(t, r, "GET", prefix+"status?incarnation="+info.Incarnation, nil, info.Token, nil, nil)
		v, e := T.Decode[T.RecipientResponse](x.b)
		if x.status != 200 || e != nil {
			t.Fatalf("status=%d decode=%v", x.status, e)
		}
		return v.Result
	}
	initial := read()
	if initial.State != "unconfigured" {
		t.Fatal("initial")
	}
	// Capture exact existing local notice bytes and snapshot before saving.
	before := r.notices.Snapshot()
	noticeBytes, noticeErr := os.ReadFile(filepath.Join(root, noticeStateName))
	in := T.RecipientInput{Version: 1, Recipient: T.RecipientReference{Kind: "codex-thread", ThreadID: "00000000-0000-7000-8000-000000000016"}, Policy: "conservative-metadata-v1"}
	body, e := T.Readable(T.RecipientConfigureRequest{Incarnation: A.UUID(info.Incarnation), ExpectedRuntimeGeneration: initial.RuntimeGeneration, RequestID: "00000000-0000-4000-8000-000000000019", Input: in})
	if e != nil {
		t.Fatal(e)
	}
	saved := request(t, r, "POST", prefix+"configure", body, "", cookie, nil)
	out, e := T.Decode[T.RecipientResponse](saved.b)
	if saved.status != 200 || e != nil || out.Result.State != "configured" || out.Result.Input == nil || *out.Result.Input != in {
		t.Fatalf("save status=%d decode=%v", saved.status, e)
	}
	afterBytes, afterErr := os.ReadFile(filepath.Join(root, noticeStateName))
	after := r.notices.Snapshot()
	if !bytes.Equal(noticeBytes, afterBytes) || (noticeErr == nil) != (afterErr == nil) || before.Generation != after.Generation || before.Phase != after.Phase {
		t.Fatal("local notice changed")
	}
	for range 2 {
		x := request(t, r, "POST", prefix+"configure", body, info.Token, nil, nil)
		assertActivityRefusal(t, x.status, x.b, 409, "CONFIGURATION_FIXED")
	}
	denied := request(t, r, "GET", prefix+"status?incarnation="+info.Incarnation, nil, "", nil, nil)
	if denied.status != 401 || bytes.Contains(denied.b, []byte(in.Recipient.ThreadID)) {
		t.Fatal("private denied disclosure")
	}
	durable, e := os.ReadFile(filepath.Join(root, recipientStateName))
	if e != nil || len(durable) > 4096 {
		t.Fatal("durable record")
	}
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
	r, e = Start(context.Background(), Options{DataDir: root, Port: info.Port})
	if e != nil {
		t.Fatal(e)
	}
	info, e = ReadClient(root)
	if e != nil {
		t.Fatal(e)
	}
	reopened := read()
	if reopened.State != "configured" || reopened.RuntimeGeneration == initial.RuntimeGeneration || reopened.ConfigurationGeneration == nil || *reopened.ConfigurationGeneration != *out.Result.ConfigurationGeneration || reopened.RequestID == nil || *reopened.RequestID != *out.Result.RequestID {
		t.Fatal("reopen changed durable correlation")
	}
	// Native root exclusion refuses a competing owner.
	other, ce := Start(context.Background(), Options{DataDir: root, Port: freePort(t)})
	if ce == nil {
		_ = other.Close()
		t.Fatal("competing root owner")
	}
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
	// A syntactically valid source sidecar copied into another root must not project.
	foreignRoot := filepath.Join(parent, "foreign")
	foreign, e := Start(context.Background(), Options{DataDir: foreignRoot, Port: freePort(t)})
	if e != nil {
		t.Fatal(e)
	}
	foreignInfo, e := ReadClient(foreignRoot)
	if e != nil {
		t.Fatal(e)
	}
	if e = foreign.Close(); e != nil {
		t.Fatal(e)
	}
	if e = publish(filepath.Join(foreignRoot, recipientStateName), durable, false); e != nil {
		t.Fatal(e)
	}
	foreign, e = Start(context.Background(), Options{DataDir: foreignRoot, Port: foreignInfo.Port})
	if e != nil {
		t.Fatal(e)
	}
	defer foreign.Close()
	foreignInfo, e = ReadClient(foreignRoot)
	if e != nil {
		t.Fatal(e)
	}
	x := request(t, foreign, "GET", foreign.boardPath()+"recipient/status?incarnation="+foreignInfo.Incarnation, nil, foreignInfo.Token, nil, nil)
	f, e := T.Decode[T.RecipientResponse](x.b)
	if x.status != 200 || e != nil || f.Result.State != "unavailable" || f.Result.Reason == nil || *f.Result.Reason != "foreign" || f.Result.Input != nil {
		t.Fatal("foreign sidecar projected")
	}
}
