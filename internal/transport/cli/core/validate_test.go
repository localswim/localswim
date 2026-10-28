package core

import "testing"

func TestValidateInvocationRequiresCompleteGrammarWithoutIO(t *testing.T) {
	if ValidateInvocation(args("card", "create", "--input", `C:\never-open\input.json`)) != nil {
		t.Fatal("pure syntax validation opened missing input")
	}
	for _, command := range [][]string{{"card", "create"}, {"unknown", "command"}, {"board", "read", "--input", "-"}} {
		if ValidateInvocation(args(command...)) == nil {
			t.Fatal("malformed grammar accepted")
		}
	}
}
