//go:build !darwin && !windows

package vmruntime

import (
	"context"
	"errors"
)

func launchNative(context.Context, command) (nativeProcess, error) {
	return nil, errors.New("unsupported VM host")
}
