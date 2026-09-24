package csvout

import "strings"

func encodeRow(cols []string) string {
	return string(appendRow(nil, cols))
}

// appendRow дописывает к dst строку CSV по §6: все ячейки в кавычках, `"` →
// `""`, LF. Байт '"' в UTF-8 не встречается внутри многобайтовых символов,
// поэтому экранирование побайтовое.
func appendRow(dst []byte, cols []string) []byte {
	for i, c := range cols {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, '"')
		for {
			j := strings.IndexByte(c, '"')
			if j < 0 {
				break
			}
			dst = append(dst, c[:j+1]...)
			dst = append(dst, '"')
			c = c[j+1:]
		}
		dst = append(dst, c...)
		dst = append(dst, '"')
	}
	return append(dst, '\n')
}
