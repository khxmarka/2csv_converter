//go:build !windows

package csvout

import "os"

func replaceFile(src, dst string) error {
	return os.Rename(src, dst)
}
