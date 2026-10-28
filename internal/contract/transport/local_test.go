package transport

import (
	"strings"
	"testing"
)

func TestLocalRuntimeAndSelectedBootstrapStrictShapes(t *testing.T) {
	const ws = `{"apiVersion":1,"mode":"workspace","workspaceId":"11111111-1111-4111-8111-111111111111"}`
	const standalone = `{"apiVersion":1,"mode":"standalone"}`
	if _, e := Decode[WorkspaceRuntimeMode]([]byte(ws)); e != nil {
		t.Fatal(e)
	}
	if _, e := Decode[StandaloneRuntimeMode]([]byte(standalone)); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{
		standalone, strings.Replace(ws, `"workspace"`, `"unknown"`, 1),
		strings.Replace(ws, `"apiVersion":1`, `"apiVersion":2`, 1),
		strings.Replace(ws, `"workspaceId":"11111111-1111-4111-8111-111111111111"`, `"workspaceId":null`, 1),
		strings.Replace(ws, `"workspaceId":"11111111-1111-4111-8111-111111111111"`, `"workspaceId":"11111111-1111-1111-8111-111111111111"`, 1),
		strings.Replace(ws, `"apiVersion":1`, `"apiVersion":1,"apiVersion":1`, 1),
		strings.TrimSuffix(ws, "}") + `,"extra":true}`,
	} {
		if _, e := Decode[WorkspaceRuntimeMode]([]byte(bad)); e == nil {
			t.Fatal("invalid runtime-mode accepted")
		}
	}
	if _, e := Decode[StandaloneRuntimeMode]([]byte(strings.Replace(ws, `"workspace"`, `"standalone"`, 1))); e == nil {
		t.Fatal("workspace field accepted in standalone discriminator")
	}
	const selected = `{"apiVersion":1,"installationId":"11111111-1111-4111-8111-111111111111","incarnation":"22222222-2222-4222-8222-222222222222"}`
	if _, e := Decode[BoardBootstrapRequest]([]byte(selected)); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{`{}`, strings.Replace(selected, `"apiVersion":1,`, ``, 1), strings.TrimSuffix(selected, "}") + `,"boardId":"33333333-3333-4333-8333-333333333333"}`} {
		if _, e := Decode[BoardBootstrapRequest]([]byte(bad)); e == nil {
			t.Fatal("invalid selection bootstrap accepted")
		}
	}
}
