// Package csvout пишет CSV по правилам §5–§6: UTF-8 без BOM, все ячейки в кавычках.
package csvout

import (
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

// FileBase делает из имени таблицы безопасную основу имени Windows-файла.
func FileBase(table string) string {
	var b strings.Builder
	for _, r := range table {
		if r < 32 || r == 0x7F || unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	s := strings.TrimRight(b.String(), " .")
	if s == "" {
		return "table"
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

func csvName(base string, n int) string {
	if n <= 0 {
		return base + ".csv"
	}
	return base + "(" + itoa(n) + ").csv"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
