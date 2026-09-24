package insert

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

const readBuf = 64 * 1024

var errUnclosedBlockComment = errors.New("незакрытый блочный комментарий")
var errIncompleteInsertTail = errors.New("незавершённый хвост INSERT")

type src struct {
	br   *bufio.Reader
	pos  int64
	line int
}

func newSrc(r io.Reader) *src {
	return newSrcSize(r, readBuf)
}

func newSrcSize(r io.Reader, size int) *src {
	return &src{
		br:   bufio.NewReaderSize(r, size),
		line: 1,
	}
}

func (s *src) skipBOM() {
	head, err := s.br.Peek(3)
	if err != nil || len(head) < 3 {
		return
	}
	if head[0] == 0xEF && head[1] == 0xBB && head[2] == 0xBF {
		_, _ = s.next()
		_, _ = s.next()
		_, _ = s.next()
	}
}

func (s *src) next() (byte, error) {
	b, err := s.br.ReadByte()
	if err != nil {
		return 0, err
	}
	s.pos++
	if b == '\n' {
		s.line++
	}
	return b, nil
}

func (s *src) peek() (byte, error) {
	xs, err := s.br.Peek(1)
	if err != nil {
		return 0, err
	}
	return xs[0], nil
}

func (s *src) peekByte(i int) (byte, bool) {
	xs, err := s.br.Peek(i + 1)
	if err != nil || len(xs) <= i {
		return 0, false
	}
	return xs[i], true
}

func (s *src) skipSpaceAndComments() error {
	for {
		b, err := s.peek()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch {
		case isSpace(b):
			if _, err := s.next(); err != nil {
				return err
			}
		case b == '-' && s.starts("--"):
			if err := s.skipLineComment(); err != nil {
				return err
			}
		case b == '/' && s.starts("/*"):
			if err := s.skipBlockComment(); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (s *src) skipTailSpaceAndComments() error {
	for {
		b, err := s.peek()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch {
		case isSpace(b):
			if _, err := s.next(); err != nil {
				return err
			}
		case b == '-' && s.starts("--"):
			if err := s.skipLineComment(); err != nil {
				return err
			}
		case b == '/' && s.starts("/*"):
			if err := s.skipBlockCommentStrict(); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (s *src) starts(lit string) bool {
	xs, err := s.br.Peek(len(lit))
	return err == nil && string(xs) == lit
}

func (s *src) skipLineComment() error {
	for {
		b, err := s.next()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if b == '\n' {
			return nil
		}
	}
}

func (s *src) skipBlockComment() error {
	if _, err := s.next(); err != nil { // /
		return err
	}
	if _, err := s.next(); err != nil { // *
		return err
	}
	for {
		b, err := s.next()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if b == '*' {
			n, err := s.peek()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if n == '/' {
				_, _ = s.next()
				return nil
			}
		}
	}
}

func (s *src) skipBlockCommentStrict() error {
	if _, err := s.next(); err != nil { // /
		return err
	}
	if _, err := s.next(); err != nil { // *
		return err
	}
	for {
		b, err := s.next()
		if err != nil {
			if err == io.EOF {
				return errUnclosedBlockComment
			}
			return err
		}
		if b != '*' {
			continue
		}
		n, err := s.peek()
		if err == io.EOF {
			return errUnclosedBlockComment
		}
		if err != nil {
			return err
		}
		if n == '/' {
			_, _ = s.next()
			return nil
		}
	}
}

// skipQuoted пропускает литерал. Обратный слэш экранирует и в '…', и в "…" —
// так же, как readString при разборе ячеек: иначе сканер и парсер находят
// конец statement в разных местах и следующий INSERT теряется.
func (s *src) skipQuoted(quote byte) error {
	if _, err := s.next(); err != nil {
		return err
	}
	for {
		b, err := s.next()
		if err != nil {
			return err
		}
		if quote != '`' && b == '\\' {
			if _, err := s.next(); err != nil && err != io.EOF {
				return err
			}
			continue
		}
		if b != quote {
			continue
		}
		if quote == '\'' || quote == '"' || quote == '`' {
			n, ok := s.peekByte(0)
			if ok && n == quote {
				_, _ = s.next()
				continue
			}
		}
		return nil
	}
}

const maxDollarTag = 64

// dollarTag возвращает открывающий тег Postgres-строки ($$ или $tag$), если
// с текущего байта начинается такой литерал. $1 и одиночный $ — не литерал.
func (s *src) dollarTag() (string, bool) {
	for i := 1; i <= maxDollarTag; i++ {
		c, ok := s.peekByte(i)
		if !ok {
			return "", false
		}
		if c == '$' {
			tag, _ := s.br.Peek(i + 1)
			return string(tag), true
		}
		if !identStart(c) && !(i > 1 && isDigit(c)) {
			return "", false
		}
	}
	return "", false
}

// skipDollarQuoted пропускает $tag$…$tag$ целиком: тела функций Postgres
// содержат INSERT, которые не являются данными дампа. Не литерал — один '$'.
func (s *src) skipDollarQuoted() error {
	tag, ok := s.dollarTag()
	if !ok {
		_, err := s.next()
		return err
	}
	for range len(tag) {
		if _, err := s.next(); err != nil {
			return err
		}
	}
	for {
		b, err := s.peek()
		if err != nil {
			return err
		}
		if b == '$' && s.starts(tag) {
			for range len(tag) {
				if _, err := s.next(); err != nil {
					return err
				}
			}
			return nil
		}
		if _, err := s.next(); err != nil {
			return err
		}
	}
}

func (s *src) skipBracketIdent() error {
	if _, err := s.next(); err != nil {
		return err
	}
	for {
		b, err := s.next()
		if err != nil {
			return err
		}
		if b == ']' {
			return nil
		}
	}
}

func (s *src) tryKeyword(kw string) (bool, error) {
	if err := s.skipSpaceAndComments(); err != nil {
		return false, err
	}
	word, ok, err := s.peekUnquotedWord()
	if err != nil || !ok {
		return false, err
	}
	if !strings.EqualFold(word, kw) {
		return false, nil
	}
	for range word {
		if _, err := s.next(); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *src) peekUnquotedWord() (string, bool, error) {
	b, err := s.peek()
	if err == io.EOF {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !identStart(b) {
		return "", false, nil
	}
	var buf []byte
	for i := 0; ; i++ {
		c, ok := s.peekByte(i)
		if !ok || !identCont(c) {
			break
		}
		buf = append(buf, c)
	}
	return string(buf), true, nil
}

func (s *src) consumeUnquotedWord() (string, error) {
	var b strings.Builder
	for {
		c, err := s.peek()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if b.Len() == 0 {
			if !identStart(c) {
				break
			}
		} else if !identCont(c) {
			break
		}
		nb, err := s.next()
		if err != nil {
			return "", err
		}
		b.WriteByte(nb)
	}
	return b.String(), nil
}

func (s *src) skipUntilSemicolon(stopAtStmt, strictComments bool) error {
	depth := 0
	seenContent := false
	for {
		b, err := s.peek()
		if err == io.EOF {
			if strictComments && (!seenContent || depth != 0) {
				return errIncompleteInsertTail
			}
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case b == ';':
			_, _ = s.next()
			if strictComments && (!seenContent || depth != 0) {
				return errIncompleteInsertTail
			}
			return nil
		case b == '-' && s.starts("--"):
			if err := s.skipLineComment(); err != nil {
				return err
			}
		case b == '/' && s.starts("/*"):
			var err error
			if strictComments {
				err = s.skipBlockCommentStrict()
			} else {
				err = s.skipBlockComment()
			}
			if err != nil {
				return err
			}
		case b == '\'', b == '"', b == '`':
			seenContent = true
			if err := s.skipQuoted(b); err != nil {
				if err == io.EOF {
					if strictComments {
						return errIncompleteInsertTail
					}
					return nil
				}
				return err
			}
		case b == '[':
			seenContent = true
			if err := s.skipBracketIdent(); err != nil {
				if err == io.EOF {
					if strictComments {
						return errIncompleteInsertTail
					}
					return nil
				}
				return err
			}
		case b == '(':
			seenContent = true
			depth++
			_, _ = s.next()
		case b == ')':
			seenContent = true
			if depth > 0 {
				depth--
			}
			_, _ = s.next()
		case b == '$':
			seenContent = true
			if err := s.skipDollarQuoted(); err != nil {
				if err == io.EOF {
					if strictComments {
						return errIncompleteInsertTail
					}
					return nil
				}
				return err
			}
		case identStart(b):
			// Слово съедается целиком: '$' внутри имени (a$b$c) не должен
			// открывать $$-литерал.
			word, _, err := s.peekUnquotedWord()
			if err != nil {
				return err
			}
			if stopAtStmt && depth == 0 && isStmtStart(word) {
				return nil
			}
			seenContent = true
			for range max(len(word), 1) {
				if _, err := s.next(); err != nil {
					return err
				}
			}
		default:
			if !isSpace(b) {
				seenContent = true
			}
			if _, err := s.next(); err != nil {
				return err
			}
		}
	}
}

func isStmtStart(word string) bool {
	switch strings.ToUpper(word) {
	case "INSERT", "CREATE", "UPDATE", "DELETE", "ALTER", "DROP",
		"REPLACE", "TRUNCATE", "USE", "LOCK", "UNLOCK", "BEGIN", "COMMIT", "WITH":
		return true
	default:
		return false
	}
}
