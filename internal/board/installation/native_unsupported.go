//go:build !windows || !amd64

package installation

import "context"

func inspectNative(context.Context) (Observation, func() error) {
	return Unqualified("PLATFORM_UNQUALIFIED"), nil
}

func probeNative(context.Context, Check, *candidateBinding) (ProbeObservation, func() error, error) {
	return nativeFailure("PLATFORM_UNQUALIFIED", true), nil, nil
}
