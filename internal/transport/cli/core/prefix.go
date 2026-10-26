package core

import "strings"

// Prefix is the shared prefix-only selector parser. Args is an owned remaining
// argument slice; special command parsers apply their own exact required scope.
type Prefix struct {
	Selected Selectors
	JSON     bool
	Args     []string
	seen     map[string]bool
}

func ParsePrefix(args []string) (p Prefix, err error) {
	p.seen = map[string]bool{}
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "--") {
		key := args[i]
		i++
		if p.seen[key] {
			return p, &Failure{"INVOCATION_OR_IO", 1}
		}
		p.seen[key] = true
		if key == "--json" {
			p.JSON = true
			continue
		}
		if key != "--installation" && key != "--board" && key != "--incarnation" && key != "--grant" || i >= len(args) {
			return p, &Failure{"INVOCATION_OR_IO", 1}
		}
		value := args[i]
		i++
		switch key {
		case "--installation":
			p.Selected.InstallationID = value
		case "--board":
			p.Selected.BoardID = value
		case "--incarnation":
			p.Selected.Incarnation = value
		case "--grant":
			p.Selected.GrantID = value
		}
	}
	p.Args = append([]string(nil), args[i:]...)
	for key := range p.seen {
		var valid bool
		switch key {
		case "--installation":
			valid = v4(p.Selected.InstallationID)
		case "--board":
			valid = uuid(p.Selected.BoardID)
		case "--incarnation":
			valid = uuid(p.Selected.Incarnation)
		case "--grant":
			valid = uuid(p.Selected.GrantID)
		default:
			valid = true
		}
		if !valid {
			return p, &Failure{"INVOCATION_OR_IO", 1}
		}
	}
	return p, nil
}

// ValidateSelectors checks raw canonical UUID spelling without unquoting,
// decoding, trimming, aliasing or selecting any default credential.
func ValidateSelectors(s Selectors, nativeLookup bool) error {
	if s.BoardID == "" || !nativeLookup && (s.InstallationID == "" || s.Incarnation == "" || s.GrantID == "") {
		return &Failure{"CREDENTIAL_SELECTION_REQUIRED", 2}
	}
	if nativeLookup && (s.InstallationID != "" || s.Incarnation != "" || s.GrantID != "") {
		return &Failure{"INVOCATION_OR_IO", 1}
	}
	if !uuid(s.BoardID) || !nativeLookup && (!v4(s.InstallationID) || !uuid(s.Incarnation) || !uuid(s.GrantID)) {
		return &Failure{"INVOCATION_OR_IO", 1}
	}
	return nil
}
