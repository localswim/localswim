package contract

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const board = "00000000-0000-4000-8000-000000000001"
const incarnation = "00000000-0000-4000-8000-000000000002"
const actor = "00000000-0000-4000-8000-000000000003"
const cardC = "00000000-0000-4000-8000-000000000004"
const cardD = "00000000-0000-4000-8000-000000000005"
const op = "00000000-0000-4000-8000-000000000006"
const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func sample(command Command, target string, input string) []byte {
	d, _ := descriptor(command)
	ct := "null"
	cr := "null"
	if d.Target != "none" {
		key := "cardId"
		if d.Target == "grant" {
			key = "grantId"
		}
		ct = fmt.Sprintf(`{"%s":%q}`, key, target)
	}
	if d.Target == "card" {
		cr = "2"
	}
	extra := ""
	if strings.HasPrefix(string(command), "grant.") {
		extra = fmt.Sprintf(`,"boardId":%q`, board)
	}
	if d.Recovery {
		extra = fmt.Sprintf(`,"command":%q,"previewHash":%q`, command, hash)
	}
	return []byte(fmt.Sprintf(`{"opId":%q,"incarnation":%q,"target":%s,"expectedRevision":7,"expectedCardRevision":%s,"input":%s%s}`, op, incarnation, ct, cr, input, extra))
}
func samples() map[Command]string {
	return map[Command]string{
		"card.create":    `{"title":"Alpha","description":""}`,
		"card.edit":      `{"description":"<>&  雪é😀\r\n\u0000\t"}`,
		"comment.add":    `{"text":"Check"}`,
		"attachment.add": fmt.Sprintf(`{"name":"a.txt","mediaLabel":"text/plain","byteLength":0,"sha256":%q}`, hash),
		"link.add":       fmt.Sprintf(`{"kind":"blocking","targetCardId":%q}`, cardD),
		"link.remove":    fmt.Sprintf(`{"linkId":%q}`, cardD),
		"review-handoff": `{}`, "approve": `{"policyVersion":1}`,
		"grant.create": fmt.Sprintf(`{"granteeActorId":%q,"kind":"human","rights":["approve","read"]}`, cardD),
		"grant.revoke": `{}`, "history.clear": `{"throughTime":"2026-10-01T00:00:00Z"}`,
		"retention.set": `{"historyDays":90,"activityDays":365,"outcomeDays":180}`,
	}
}
func TestFreezeAllCommands(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Commands() {
		t.Run(string(d.Command), func(t *testing.T) {
			target := ""
			if d.Target != "none" {
				target = cardC
			}
			wire := sample(d.Command, target, samples()[d.Command])
			i, e := Freeze(wire, ResolvedRoute{d.Command, board, target}, AuthenticatedActor{actor})
			if e != nil {
				t.Fatal(e)
			}
			if seen[i.Fingerprint()] {
				t.Fatal("duplicate semantic hash")
			}
			seen[i.Fingerprint()] = true
			v, e := DecodeJSON(i.CanonicalBytes())
			if e != nil {
				t.Fatal(e)
			}
			m := v.(map[string]any)
			if len(m) != 10 || m["actorId"] != actor {
				t.Fatal("incomplete semantic envelope")
			}
			p := m["payload"].(map[string]any)
			typed, e := i.Input()
			if e != nil {
				t.Fatal(e)
			}
			typedJSON, e := json.Marshal(typed)
			if e != nil {
				t.Fatal(e)
			}
			typedValue, e := DecodeJSON(typedJSON)
			if e != nil {
				t.Fatal(e)
			}
			typedBytes, e := Canonical(typedValue)
			if e != nil {
				t.Fatal(e)
			}
			expectedInput := any(p)
			if d.Recovery {
				expectedInput = p["input"]
			}
			expectedBytes, e := Canonical(expectedInput)
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(typedBytes, expectedBytes) {
				t.Fatal("Go typed input and descriptor disagree")
			}
			if d.Recovery {
				if p["previewHash"] != hash || p["input"] == nil {
					t.Fatal("recovery payload binding")
				}
			}
			if d.Command == "card.edit" && p["priority"] != nil {
				t.Fatal("omitted field not null")
			}
			if d.Command == "card.create" && p["priority"] != nil {
				t.Fatal("priority default entered canonical intent")
			}
			copy := i.CanonicalBytes()
			copy[0] = '!'
			if i.CanonicalBytes()[0] != '{' {
				t.Fatal("mutable frozen bytes")
			}
		})
	}
}
func TestFullTargetAndActorBinding(t *testing.T) {
	freeze := func(c Command, target, peer, aid string) Intent {
		t.Helper()
		input := samples()[c]
		if c == "link.add" {
			input = fmt.Sprintf(`{"kind":"blocking","targetCardId":%q}`, peer)
		}
		i, e := Freeze(sample(c, target, input), ResolvedRoute{c, board, target}, AuthenticatedActor{aid})
		if e != nil {
			t.Fatal(e)
		}
		return i
	}
	for _, c := range []Command{"card.edit", "link.add", "grant.revoke"} {
		a := freeze(c, cardC, cardD, actor)
		b := freeze(c, cardD, cardC, actor)
		if a.Fingerprint() == b.Fingerprint() || bytes.Equal(a.CanonicalBytes(), b.CanonicalBytes()) {
			t.Fatal("target/reversed-link collision", c)
		}
	}
	a := freeze("card.edit", cardC, cardD, actor)
	b := freeze("card.edit", cardC, cardD, cardD)
	if a.Fingerprint() == b.Fingerprint() {
		t.Fatal("actor omitted")
	}
	for _, c := range []Command{"link.remove", "grant.create"} {
		input := samples()[c]
		other := strings.Replace(input, cardD, actor, 1)
		x, e := Freeze(sample(c, cardC, input), ResolvedRoute{c, board, cardC}, AuthenticatedActor{actor})
		if c == "grant.create" {
			x, e = Freeze(sample(c, "", input), ResolvedRoute{c, board, ""}, AuthenticatedActor{actor})
		}
		if e != nil {
			t.Fatal(e)
		}
		target := cardC
		if c == "grant.create" {
			target = ""
		}
		y, e := Freeze(sample(c, target, other), ResolvedRoute{c, board, target}, AuthenticatedActor{actor})
		if e != nil {
			t.Fatal(e)
		}
		if x.Fingerprint() == y.Fingerprint() {
			t.Fatal("payload resource identity omitted", c)
		}
	}
	if _, e := Freeze(sample("card.edit", cardC, samples()["card.edit"]), ResolvedRoute{"card.edit", board, cardD}, AuthenticatedActor{actor}); e == nil {
		t.Fatal("wrong route target accepted")
	}
}

func TestDescriptorsCannotChangeAuthority(t *testing.T) {
	d := Commands()[0]
	d.Input.Fields["title"].Type.Format = ""
	d.Input.Fields["priority"].Type.Enum[0] = "invented"
	r := RequestType(Commands()[0])
	r.Fields["opId"].Type.Format = ""
	result := ResultType("card.create")
	result.Fields["cardId"].Type.Format = ""
	if _, e := Freeze(sample("card.create", "", `{"title":" ","description":""}`), ResolvedRoute{"card.create", board, ""}, AuthenticatedActor{actor}); e == nil {
		t.Fatal("exported descriptor mutated validation")
	}
	bad := strings.Replace(string(sample("card.create", "", samples()["card.create"])), op, "bad", 1)
	if _, e := Freeze([]byte(bad), ResolvedRoute{"card.create", board, ""}, AuthenticatedActor{actor}); e == nil {
		t.Fatal("exported request descriptor mutated UUID validation")
	}
}

func TestStrictJSONRejections(t *testing.T) {
	bad := []string{`{"a":1,"a":2}`, `{"a":1,"\u0061":2}`, `{"x":{"y":0,"y":1}}`, `"\ud800"`, `"\udfff"`, `"\ud800\u0041"`, `9007199254740992`, `1e0`, `1.0`, `-0`, `-1`, `01`, `{} {}`, "\xef\xbb\xbf{}", "\"\xff\"", strings.Repeat("[", 33) + strings.Repeat("]", 33)}
	for _, s := range bad {
		if _, e := DecodeJSON([]byte(s)); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	for _, s := range []string{`null`, `0`, `9007199254740991`, `"\ud83d\ude00"`, `"\\ud800"`} {
		v, e := DecodeJSON([]byte(s))
		if e != nil {
			t.Fatal(s, e)
		}
		if _, e = Canonical(v); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := Canonical(map[string]any{"é": nil}); e == nil {
		t.Fatal("non-ASCII schema key")
	}
}

func TestFixedSemanticBytes(t *testing.T) {
	want := `{"actorId":"00000000-0000-4000-8000-000000000003","boardId":"00000000-0000-4000-8000-000000000001","command":"comment.add","expectedCardRevision":2,"expectedRevision":7,"fingerprintVersion":1,"incarnation":"00000000-0000-4000-8000-000000000002","opId":"00000000-0000-4000-8000-000000000006","payload":{"text":"Check"},"target":{"cardId":"00000000-0000-4000-8000-000000000004"}}`
	i, e := Freeze(sample("comment.add", cardC, `{"text":"Check"}`), ResolvedRoute{"comment.add", board, cardC}, AuthenticatedActor{actor})
	if e != nil {
		t.Fatal(e)
	}
	if string(i.CanonicalBytes()) != want || i.Fingerprint() != "af7fa3673b6f1df76c28081f1397361e7aebac4dc332266fa2686aee238d9163" {
		t.Fatal("fixed semantic bytes/hash changed")
	}
}
func TestRequestRejections(t *testing.T) {
	base := string(sample("card.edit", cardC, `{"description":"Draft"}`))
	bad := []string{strings.Replace(base, `"description":"Draft"`, `"description":null`, 1), strings.Replace(base, `"description":"Draft"`, `"actorId":"x"`, 1), strings.Replace(base, `"description":"Draft"`, `"lane":"Completed"`, 1), strings.Replace(base, `"expectedRevision":7`, `"expectedRevision":9007199254740992`, 1), strings.Replace(base, `"expectedCardRevision":2`, `"expectedCardRevision":null`, 1), strings.Replace(base, `"target":{"cardId":"`+cardC+`"}`, `"target":null`, 1), strings.Replace(base, `"input":`, `"actorId":"`+actor+`","input":`, 1), strings.Replace(base, `"input":`, `"token":"secret","input":`, 1)}
	for _, s := range bad {
		if _, e := Freeze([]byte(s), ResolvedRoute{"card.edit", board, cardC}, AuthenticatedActor{actor}); e == nil {
			t.Fatal("invalid request accepted")
		}
	}
	for _, input := range []string{`{"title":"  "}`, `{"title":"` + strings.Repeat("😀", 201) + `"}`, `{"description":"` + strings.Repeat("x", 65537) + `"}`} {
		if _, e := Freeze(sample("card.edit", cardC, input), ResolvedRoute{"card.edit", board, cardC}, AuthenticatedActor{actor}); e == nil {
			t.Fatal("text limit ignored")
		}
	}
	for _, input := range []string{fmt.Sprintf(`{"granteeActorId":%q,"kind":"human","rights":["read","approve"]}`, actor), fmt.Sprintf(`{"granteeActorId":%q,"kind":"human","rights":["read","read"]}`, actor)} {
		if _, e := Freeze(sample("grant.create", "", input), ResolvedRoute{"grant.create", board, ""}, AuthenticatedActor{actor}); e == nil {
			t.Fatal("unsorted/duplicate rights")
		}
	}
}
func TestAcceptedD02CanonicalVectors(t *testing.T) {
	root := filepath.Join("..", "..", "fixtures", "contracts", "archive-recovery")
	data, e := os.ReadFile(filepath.Join(root, "vectors.json"))
	if e != nil {
		t.Fatal(e)
	}
	var index struct {
		Vectors []struct{ Name, Path, Hex, SHA256 string }
	}
	if e = json.Unmarshal(data, &index); e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, v := range index.Vectors {
		if v.Hex == "" {
			continue
		}
		raw, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(v.Path)))
		if e != nil {
			t.Fatal(e)
		}
		value, e := DecodeJSON(raw)
		if e != nil {
			t.Fatal(v.Name, e)
		}
		b, e := Canonical(value)
		if e != nil {
			t.Fatal(e)
		}
		h, e := Hash(value)
		if e != nil {
			t.Fatal(e)
		}
		if hex.EncodeToString(b) != v.Hex || h != v.SHA256 {
			t.Fatal("D02 vector mismatch", v.Name)
		}
		count++
	}
	if count < 4 {
		t.Fatal("missing mandatory D02 vectors")
	}
}
func TestGeneratedDeterministic(t *testing.T) {
	a, e := GeneratedFiles()
	if e != nil {
		t.Fatal(e)
	}
	b, e := GeneratedFiles()
	if e != nil {
		t.Fatal(e)
	}
	for path, want := range a {
		if !bytes.Equal(want, b[path]) {
			t.Fatal("nondeterministic generation")
		}
		got, e := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(path)))
		if e != nil || !bytes.Equal(got, want) {
			t.Fatal("generated drift", path)
		}
	}
}
func TestRetainedResults(t *testing.T) {
	for _, d := range Commands() {
		r := ResultType(d.Command)
		value := map[string]any{}
		for k := range r.Fields {
			if strings.HasSuffix(k, "Version") || k == "coverageRevision" {
				value[k] = json.Number("1")
			} else {
				value[k] = cardC
			}
		}
		b, e := Canonical(value)
		if e != nil {
			t.Fatal(e)
		}
		if e = ValidateResult(b, d.Command, true); e != nil {
			t.Fatal(d.Command, e)
		}
	}
	if e := ValidateResult([]byte(`{"error":{"code":"NOT_FOUND","message":"Missing","field":null},"conflict":null}`), "card.edit", false); e != nil {
		t.Fatal(e)
	}
	if e := ValidateResult([]byte(`{"grantId":"`+cardC+`","token":"secret"}`), "grant.create", true); e == nil {
		t.Fatal("secret field accepted")
	}
}
