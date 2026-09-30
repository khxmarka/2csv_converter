package csvout

import "strings"

func encodeRow(cols []string) string {
	return string(appendRow(nil, cols))
}

// appendRow writes a fully quoted CSV record with LF endings. Byte-wise quote
// escaping is safe because '"' cannot occur inside a multibyte UTF-8 sequence.
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
