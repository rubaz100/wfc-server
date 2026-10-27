package certs

import (
	"fmt"
	"os"
	"runtime"
)

func validateKeyPermissions(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("private key %s must be a regular file with mode 0600", path)
	}
	return nil
}
