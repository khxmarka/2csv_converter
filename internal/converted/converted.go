// Package converted пишет converted.txt в корне обхода (§7 политики).
package converted

import (
	"os"
	"path/filepath"
	"strings"
)

const FileName = "converted.txt"

// Path возвращает C:\Source\db\converted.txt или аналог для тестового корня.
func Path(root string) string {
	return filepath.Join(root, FileName)
}

// Encode — UTF-8 без BOM, одно имя на строку, LF. Пустой список → пустой файл.
func Encode(names []string) []byte {
	if len(names) == 0 {
		return []byte{}
	}
	var b strings.Builder
	b.Grow(len(names) * 16)
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// Write перезаписывает converted.txt в root. Даже при пустом names файл создаётся.
func Write(root string, names []string) error {
	return os.WriteFile(Path(root), Encode(names), 0o644)
}
