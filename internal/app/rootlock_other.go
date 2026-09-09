//go:build !windows

package app

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func acquirePlatformRootLock(key string) (func(), error) {
	name := fmt.Sprintf("2csv-%x.lock", sha256.Sum256([]byte(key)))
	path := filepath.Join(os.TempDir(), name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
