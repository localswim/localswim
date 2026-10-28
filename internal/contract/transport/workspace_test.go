package transport

import (
	"strings"
	"testing"
)

func TestWorkspaceControlStrictBoundaries(t *testing.T) {
	const request = `{"apiVersion":1,"expectedConfigRevision":1,"projectKey":"harbor","boardKey":"main","label":"Main board","dataDir":"C:\\p\\main","action":"register","expectedIdentity":null}`
	if _, e := Decode[WorkspaceBoardRequest]([]byte(request)); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{strings.Replace(request, `"register"`, `"adopt"`, 1), strings.Replace(request, `"harbor"`, `"Harbor"`, 1), strings.Replace(request, `"main"`, `"bad--key"`, 1), strings.Replace(request, `"expectedConfigRevision":1`, `"expectedConfigRevision":0`, 1), strings.Replace(request, `"expectedIdentity":null`, `"expectedIdentity":null,"expectedIdentity":null`, 1), strings.Replace(request, `,"expectedIdentity":null`, ``, 1), strings.Replace(request, `"label":"Main board"`, `"label":"`+strings.Repeat("a", 201)+`"`, 1)} {
		if _, e := Decode[WorkspaceBoardRequest]([]byte(bad)); e == nil {
			t.Fatal("invalid control accepted")
		}
	}
}

func TestWorkspaceUnavailableRefusesPartialReadyFacts(t *testing.T) {
	for _, body := range []string{
		`{"projectKey":"harbor","boardKey":"main","label":"Main","availability":"unavailable","identity":null,"memberEpoch":"11111111-1111-4111-8111-111111111111"}`,
		`{"projectKey":"harbor","boardKey":"main","label":"Main","availability":"ready","identity":null,"memberEpoch":null}`,
		`{"projectKey":"harbor","boardKey":"main","label":"Main","availability":"unavailable","identity":{"installationId":"11111111-1111-4111-8111-111111111111","boardId":"22222222-2222-4222-8222-222222222222","incarnation":"33333333-3333-4333-8333-333333333333"},"memberEpoch":null}`,
	} {
		if _, e := Decode[WorkspaceMember]([]byte(body)); e == nil {
			t.Fatal("inconsistent readiness accepted")
		}
	}
}
