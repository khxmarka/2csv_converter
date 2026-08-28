package insert

import "strings"

const identJunk = "`\"'[]\\/"

func cleanIdent(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if strings.ContainsRune(identJunk, r) {
			return -1
		}
		return r
	}, s))
}

func identStart(b byte) bool {
	return b >= 0x80 || b == '_' || b == '@' || isAlpha(b)
}

func identCont(b byte) bool {
	return identStart(b) || isDigit(b) || b == '$'
}

func isAlpha(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}
