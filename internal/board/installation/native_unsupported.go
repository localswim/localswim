//go:build !windows || !amd64

package installation

import "context"

func inspectNative(context.Context) (Observation, func() error) {
	return Unqualified("PLATFORM_UNQUALIFIED"), nil
}
