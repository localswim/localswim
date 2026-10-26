//go:build !windows || !amd64

package https

import (
	"context"
	"net"
)

func listenBoth(context.Context, string) ([]net.Listener, error) { return nil, ErrUnavailable }
