// Package csvout пишет CSV по правилам §5–§6: UTF-8 без BOM, все ячейки в кавычках.
package csvout

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

var reservedStems = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {},
	"COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {},
	"LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
}

const maxCSVBaseUTF16 = 251 // 255 UTF-16 units minus ".csv"

// FileBase делает из имени таблицы безопасную основу имени Windows-файла.
// Пустое после чистки → "table".
func FileBase(table string) string {
	return FileBaseDefault(table, "table")
}

// FileBaseDefault — тот же санитайз Windows, что FileBase; пустой итог → empty.
func FileBaseDefault(name, empty string) string {
	if empty == "" {
		empty = "table"
	}
	var b strings.Builder
	for _, r := range name {
		if r < 32 || r == 0x7F || unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	s := strings.TrimRight(b.String(), " .")
	if s == "" {
		return empty
	}
	stem := s
	if i := strings.IndexByte(s, '.'); i >= 0 {
		stem = s[:i]
	}
	if _, bad := reservedStems[strings.ToUpper(stem)]; bad {
		return "_" + s
	}
	return s
}

func limitCSVBase(base string) string {
	return limitCSVBaseSuffix(base, "")
}

// limitCSVBaseSuffix ограничивает основу вместе с суффиксом вроде «_2».
// Суффикс сохраняется; при усечении к префиксу добавляется тот же hash, что у limitCSVBase.
func limitCSVBaseSuffix(base, suffix string) string {
	if utf16Units(base)+utf16Units(suffix) <= maxCSVBaseUTF16 {
		return base + suffix
	}
	sum := sha256.Sum256([]byte(base))
	tail := "~" + hex.EncodeToString(sum[:8]) + suffix
	prefixLimit := maxCSVBaseUTF16 - utf16Units(tail)
	if prefixLimit < 0 {
		prefixLimit = 0
	}
	var b strings.Builder
	units := 0
	for _, r := range base {
		n := 1
		if r > 0xFFFF {
			n = 2
		}
		if units+n > prefixLimit {
			break
		}
		b.WriteRune(r)
		units += n
	}
	b.WriteString(tail)
	return b.String()
}

func utf16Units(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}
