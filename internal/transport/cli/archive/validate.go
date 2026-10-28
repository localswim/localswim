package archive

import "github.com/localswim/localswim/internal/transport/cli/core"

// ValidateInvocation checks the owning command grammar without opening files,
// credentials, evidence or a connection. Selection here is syntax, not authority.
func ValidateInvocation(args []string) error {
	_, code, exit := parse(args)
	if code != "" {
		return &core.Failure{Code: code, Exit: exit}
	}
	return nil
}
