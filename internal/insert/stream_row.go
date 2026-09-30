package insert

import (
	"fmt"
	"io"
	"strings"
)

// emitRow parses one tuple into cw. When maxCells is positive, surplus values
// are consumed before ErrRowSurplus is returned.
func emitRow(s *src, cw CellWriter, maxCells int) error {
	if err := s.skipSpaceAndComments(); err != nil {
		return err
	}
	b, err := s.peek()
	if err != nil {
		return err
	}
	if b != '(' {
		return fmt.Errorf("ожидалась '(' строки VALUES")
	}
	_, _ = s.next()

	n := 0
	expectValue := true
	surplus := false
	for {
		if err := s.skipSpaceAndComments(); err != nil {
			return err
		}
		b, err := s.peek()
		if err != nil {
			return err
		}
		if b == ')' {
			if expectValue && n > 0 {
				if !surplus {
					if maxCells > 0 && n >= maxCells {
						surplus = true
					} else {
						if err := cw.Missing(); err != nil {
							return err
						}
						n++
					}
				}
			}
			_, _ = s.next()
			if surplus {
				return ErrRowSurplus
			}
			if maxCells > 0 && n > maxCells {
				return ErrRowSurplus
			}
			return nil
		}
		if b == ',' {
			if expectValue {
				if !surplus {
					if maxCells > 0 && n >= maxCells {
						surplus = true
					} else {
						if err := cw.Missing(); err != nil {
							return err
						}
						n++
					}
				}
			}
			_, _ = s.next()
			expectValue = true
			continue
		}
		if !expectValue {
			return fmt.Errorf("между значениями нет запятой")
		}
		if surplus || (maxCells > 0 && n >= maxCells) {
			surplus = true
			if err := skipValue(s); err != nil {
				return err
			}
			n++
			expectValue = false
			continue
		}
		if err := emitValue(s, cw); err != nil {
			return err
		}
		n++
		expectValue = false
	}
}

func emitValue(s *src, cw CellWriter) error {
	if err := s.skipSpaceAndComments(); err != nil {
		return err
	}
	b, err := s.peek()
	if err != nil {
		return err
	}
	switch b {
	case '\'', '"':
		return cw.Text(func(w io.Writer) error {
			return streamStringContent(s, b, w)
		})
	case 'N', 'n', 'E', 'e':
		// MSSQL N'…' and Postgres E'…' use ordinary string contents without the
		// prefix. Other words such as NOW() and NULL are not string literals.
		if q, ok := s.peekByte(1); ok && q == '\'' {
			_, _ = s.next()
			return cw.Text(func(w io.Writer) error {
				return streamStringContent(s, '\'', w)
			})
		}
	}

	word, ok, err := s.peekUnquotedWord()
	if err != nil {
		return err
	}
	if ok && strings.EqualFold(word, "NULL") {
		for range word {
			_, _ = s.next()
		}
		return cw.Null()
	}

	// Raw numbers and expressions are expected to be short enough to buffer.
	text, err := readRawValue(s)
	if err != nil {
		return err
	}
	if text == "" {
		return cw.Missing()
	}
	return cw.Text(func(w io.Writer) error {
		_, err := io.WriteString(w, text)
		return err
	})
}

// discardCells consumes surplus row values without retaining their text.
type discardCells struct{}

func (discardCells) Null() error    { return nil }
func (discardCells) Missing() error { return nil }
func (discardCells) Text(write func(io.Writer) error) error {
	return write(io.Discard)
}

func skipValue(s *src) error {
	return emitValue(s, discardCells{})
}

// streamStringContent decodes an SQL string whose opening quote has not yet been consumed.
func streamStringContent(s *src, quote byte, w io.Writer) error {
	if _, err := s.next(); err != nil {
		return err
	}
	var one [1]byte
	for {
		// Copy plain runs in chunks because byte-at-a-time writes through the writer
		// chain are substantially slower.
		if chunk := s.plainRun(quote); len(chunk) > 0 {
			if _, err := w.Write(chunk); err != nil {
				return err
			}
			s.advance(chunk)
			continue
		}
		c, err := s.next()
		if err != nil {
			return err
		}
		if c == '\\' {
			n, err := s.peek()
			if err == io.EOF {
				one[0] = '\\'
				_, err = w.Write(one[:])
				return err
			}
			if err != nil {
				return err
			}
			_, _ = s.next()
			if n == quote || n == '\\' {
				one[0] = n
				if _, err := w.Write(one[:]); err != nil {
					return err
				}
				continue
			}
			one[0] = '\\'
			if _, err := w.Write(one[:]); err != nil {
				return err
			}
			one[0] = n
			if _, err := w.Write(one[:]); err != nil {
				return err
			}
			continue
		}
		if c == quote {
			n, err := s.peek()
			if err == nil && n == quote {
				_, _ = s.next()
				one[0] = quote
				if _, err := w.Write(one[:]); err != nil {
					return err
				}
				continue
			}
			return nil
		}
		one[0] = c
		if _, err := w.Write(one[:]); err != nil {
			return err
		}
	}
}
