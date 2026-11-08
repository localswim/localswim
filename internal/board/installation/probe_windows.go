//go:build windows && amd64

package installation

import (
	"context"
	"errors"

	"github.com/localswim/localswim/internal/nativecustody"
	"golang.org/x/sys/windows"
)

func probeNative(ctx context.Context, check Check, binding *candidateBinding) (ProbeObservation, func() error, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nativeFailure("EXIT_FAILED", true), nil, nil
	}
	closeToken := func() error { return token.Close() }
	user, err := token.GetTokenUser()
	if err != nil {
		return nativeFailure("EXIT_FAILED", true), closeOnce(closeToken), nil
	}
	profile, err := token.GetUserProfileDirectory()
	if err != nil {
		return nativeFailure("EXIT_FAILED", true), closeOnce(closeToken), nil
	}
	var candidate *nativeCandidate
	observation, closePins := resolveProfile(ctx, profile, user.User.Sid, &candidate)
	cleanup := closeOnce(closePins, closeToken)
	if observation.State != "candidate" {
		out, err := preparationObservation(ctx, observation)
		return out, cleanup, err
	}
	if err := binding.choose(candidate.fingerprint()); err != nil {
		return ProbeObservation{}, cleanup, err
	}
	workspace, workspaceCleanup, err := createNativeWorkspace(ctx, profile, user.User.Sid, binding.runtime, binding.probe)
	cleanup = closeOnce(workspaceCleanup, closePins, closeToken)
	if err != nil {
		return nativeFailure("EXIT_FAILED", true), cleanup, nil
	}
	var phaseError error
	guarded := func(work context.Context) error {
		if err := check(work); err != nil {
			if phaseError == nil {
				phaseError = err
			}
			return err
		}
		if err := candidate.recheck(work); err != nil {
			if phaseError == nil {
				phaseError = err
			}
			return err
		}
		if err := workspace.recheck(); err != nil {
			if phaseError == nil {
				phaseError = err
			}
			return err
		}
		return nil
	}
	result := nativecustody.ExecuteVersion(ctx, candidate.executable.path, workspace.root, guarded)
	if errors.Is(phaseError, ErrIdentityChanged) {
		return ProbeObservation{}, cleanup, ErrIdentityChanged
	}
	if errors.Is(phaseError, errNativeEvidence) {
		if result.Code == "TIMED_OUT" {
			return nativeFailure("TIMED_OUT", true), cleanup, nil
		}
		return nativeFailure("EXIT_FAILED", true), cleanup, nil
	}
	if phaseError != nil && !errors.Is(phaseError, context.Canceled) && !errors.Is(phaseError, context.DeadlineExceeded) {
		return ProbeObservation{}, cleanup, phaseError
	}
	// Native completion does not release pinned installation/work-root custody.
	// Recheck both before any captured bytes can become a positive observation.
	if ctx.Err() == nil {
		if err := guarded(ctx); err != nil {
			if errors.Is(err, errNativeEvidence) {
				return nativeFailure("EXIT_FAILED", true), cleanup, nil
			}
			return ProbeObservation{}, cleanup, err
		}
	}
	if result.Code != "OK" || result.ExitCode != 0 || !result.Joined {
		return nativeFailure(result.Code, result.Joined), cleanup, nil
	}
	if ctx.Err() != nil {
		return cancellationObservation(ctx), cleanup, nil
	}
	return parseIdentity(result.Stdout, result.Stderr, candidate.version), cleanup, nil
}
