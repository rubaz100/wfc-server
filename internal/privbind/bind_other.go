//go:build !darwin

package privbind

import (
	"context"
	"errors"
)

func privilegedBind(context.Context, []Spec, string) (*Sockets, error) {
	return nil, errors.New("administrator socket binding is only implemented on macOS; choose high ports or adjust the operating system port permissions")
}

func HelperMain([]string) int {
	return 1
}
