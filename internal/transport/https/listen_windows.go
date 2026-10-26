//go:build windows && amd64

package https

import (
	"context"
	"golang.org/x/sys/windows"
	"net"
	"syscall"
)

func listenBoth(ctx context.Context, port string) ([]net.Listener, error) {
	lc := net.ListenConfig{Control: func(network, address string, raw syscall.RawConn) error {
		var e error
		if x := raw.Control(func(fd uintptr) {
			e = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, ^windows.SO_REUSEADDR, 1)
			if e == nil && network == "tcp6" {
				e = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IPV6, windows.IPV6_V6ONLY, 1)
			}
		}); x != nil {
			return x
		}
		return e
	}}
	a, e := lc.Listen(ctx, "tcp4", net.JoinHostPort("127.0.0.1", port))
	if e != nil {
		return nil, ErrUnavailable
	}
	b, e := lc.Listen(ctx, "tcp6", net.JoinHostPort("::1", port))
	if e != nil {
		a.Close()
		return nil, ErrUnavailable
	}
	return []net.Listener{a, b}, nil
}
