//go:build !windows || !amd64

package local

import "github.com/localswim/localswim/internal/board/notices"

func noticeNativeSignal(string, string) (string, error) {
	return "", notices.ErrStorage
}
