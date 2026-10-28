package archive

import "testing"

func TestValidateInvocationRequiresCompleteGrammarWithoutIO(t *testing.T) {
	prefix := []string{"--installation", "00000000-0000-4000-8000-000000000001", "--board", "00000000-0000-4000-8000-000000000002", "--incarnation", "00000000-0000-4000-8000-000000000003", "--grant", "00000000-0000-4000-8000-000000000004"}
	if ValidateInvocation(append(append([]string{}, prefix...), "export", "create", "--destination", `C:\never-open\export.zip`)) != nil {
		t.Fatal("pure syntax validation opened missing destination")
	}
	for _, command := range [][]string{{"export", "create"}, {"export", "download", "--output", "x"}, {"attachment", "add", "00000000-0000-4000-8000-000000000005", "--input", "-"}} {
		if ValidateInvocation(append(append([]string{}, prefix...), command...)) == nil {
			t.Fatal("malformed grammar accepted")
		}
	}
}
