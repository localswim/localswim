//go:build !windows && !linux

package v2

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

func publicationChildExited(ctx context.Context, pid int) (bool, error) {
	output, err := publicationProcessList(ctx, 1024, "-p", strconv.Itoa(pid), "-o", "stat=")
	if err != nil {
		return false, err
	}
	fields := strings.Fields(output)
	if len(fields) != 1 {
		return false, errors.New("publication root process state unavailable")
	}
	return fields[0][0] == 'Z', nil
}
