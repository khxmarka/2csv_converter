package csvout

import (
	"strings"
	"unicode/utf8"
)

func encodeRow(cols []string) string {
	var b strings.Builder
	// грубая оценка: кавычки + запятые + экранирование
	size := 0
	for _, c := range cols {
		size += 2 + len(c) + strings.Count(c, `"`) + 1
	}
	b.Grow(size)
	for i, c := range cols {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		if strings.Contains(c, `"`) {
			writeEscaped(&b, c)
		} else {
			b.WriteString(c)
		}
		b.WriteByte('"')
	}
	b.WriteByte('\n')
	return b.String()
}

func writeEscaped(b *strings.Builder, s string) {
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == '"' {
			b.WriteString(`""`)
		} else {
			b.WriteString(s[i : i+n])
		}
		i += n
	}
}
