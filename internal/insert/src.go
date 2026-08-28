package insert

import (
	"bufio"
	"io"
	"strings"
)

const readBuf = 64 * 1024

type src struct {
	br   *bufio.Reader
	pos  int64
	line int
	col  int
}

func newSrc(r io.Reader) *src {
	return &src{
		br:   bufio.NewReaderSize(r, readBuf),
		line: 1,
		col:  1,
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
		s.col = 1
	} else {
		s.col++
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

func (s *src) eof() bool {
	_, err := s.peek()
	return err != nil
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

func (s *src) skipQuoted(quote byte) error {
	if _, err := s.next(); err != nil {
		return err
	}
	for {
		b, err := s.next()
		if err != nil {
			return err
		}
		if quote == '\'' && b == '\\' {
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

func (s *src) skipUntilSemicolon(stopAtStmt bool) error {
	depth := 0
	for {
		b, err := s.peek()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case b == ';':
			_, _ = s.next()
			return nil
		case b == '-' && s.starts("--"):
			if err := s.skipLineComment(); err != nil {
				return err
			}
		case b == '/' && s.starts("/*"):
			if err := s.skipBlockComment(); err != nil {
				return err
			}
		case b == '\'', b == '"', b == '`':
			if err := s.skipQuoted(b); err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
		case b == '[':
			if err := s.skipBracketIdent(); err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
		case b == '(':
			depth++
			_, _ = s.next()
		case b == ')':
			if depth > 0 {
				depth--
			}
			_, _ = s.next()
		case stopAtStmt && depth == 0 && identStart(b):
			word, ok, err := s.peekUnquotedWord()
			if err != nil {
				return err
			}
			if ok && isStmtStart(word) {
				return nil
			}
			if _, err := s.next(); err != nil {
				return err
			}
		default:
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
