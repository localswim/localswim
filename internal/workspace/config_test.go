package workspace

import (
	"encoding/json"
	T "github.com/localswim/localswim/internal/contract/transport"
	"strings"
	"testing"
)

func TestConfigurationStrictBoundsAndDuplicateMembership(t *testing.T) {
	c := newConfiguration(8820)
	c.Projects = []project{{"harbor", "Harbor", `C:\p`, "project-physical"}}
	c.Members = []member{{"harbor", "main", "Main", `C:\p\main`, "member-physical", T.WorkspaceIdentity{InstallationID: "11111111-1111-4111-8111-111111111111", BoardID: "22222222-2222-4222-8222-222222222222", Incarnation: "33333333-3333-4333-8333-333333333333"}}}
	b, e := configBytes(c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = decodeConfig(b); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{strings.Replace(string(b), `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1), strings.Replace(string(b), `"schemaVersion":1`, `"schemaVersion":2`, 1), strings.Replace(string(b), `"projects":[`, `"extra":1,"projects":[`, 1), strings.Replace(string(b), `"port":8820`, `"port":0`, 1), strings.Replace(string(b), `"configRevision":1`, `"configRevision":0`, 1), strings.Replace(string(b), `"harbor"`, `"Harbor"`, 1), strings.Repeat(" ", int(ConfigLimit)+1)} {
		if _, e = decodeConfig([]byte(bad)); e == nil {
			t.Fatal("invalid config accepted")
		}
	}
	for _, change := range []func(*configuration){
		func(x *configuration) { x.Members = append(x.Members, x.Members[0]) },
		func(x *configuration) {
			m := x.Members[0]
			m.BoardKey = "other"
			m.PhysicalKey = "different"
			m.DataDir = `C:\p\other`
			x.Members = append(x.Members, m)
		},
		func(x *configuration) {
			m := x.Members[0]
			m.BoardKey = "other"
			m.Identity.BoardID = "44444444-4444-4444-8444-444444444444"
			x.Members = append(x.Members, m)
		},
		func(x *configuration) { x.Members[0].DataDir = `C:\outside\main` },
		func(x *configuration) { x.Projects[0].Root = `\\host\share` },
		func(x *configuration) { x.Projects[0].Root = `C:\p\..\outside` },
		func(x *configuration) { x.Members = make([]member, MemberLimit+1) },
	} {
		copyBytes, _ := json.Marshal(c)
		var x configuration
		_ = json.Unmarshal(copyBytes, &x)
		change(&x)
		if _, e = configBytes(x); e == nil {
			t.Fatal("invalid membership accepted")
		}
	}
}
