//go:build !windows || !amd64

package admin

import (
	"github.com/localswim/localswim/internal/transport/cli/core"
	"io"
)

func openCodeConsole(io.Reader) (codeConsole, error) {
	return nil, &core.Failure{Code: "UNAVAILABLE", Exit: 3}
}
