package core

// ValidateInvocation checks the owning command grammar without opening input,
// credentials, evidence or a connection. Selection here is syntax, not authority.
func ValidateInvocation(args []string) error {
	_, code, exit := parse(args)
	if code != "" {
		return &Failure{Code: code, Exit: exit}
	}
	return nil
}
