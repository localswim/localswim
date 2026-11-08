//go:build !windows || !amd64

package core

import "errors"

func readRecipientInput(string) ([]byte, error) {
	return nil, errors.New("private recipient input unavailable")
}
