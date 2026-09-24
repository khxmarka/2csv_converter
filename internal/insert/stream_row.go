package insert

import (
	"fmt"
	"io"
	"strings"
)

// emitRow разбирает одну tuple (...) и отдаёт ячейки в cw.
// maxCells > 0: лишние значения дочитываются и возвращается ErrRowSurplus.
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
		quote := b
		return cw.Text(func(w io.Writer) error {
			return streamStringContent(s, quote, w)
		})
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

	return cw.Text(func(w io.Writer) error {
		text, err := readRawValue(s)
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, text)
		return err
	})
}

func skipValue(s *src) error {
	_, err := parseValue(s)
	return err
}

// streamStringContent читает SQL-строку (открывающая кавычка ещё не съедена)
// и пишет декодированное содержимое в w.
func streamStringContent(s *src, quote byte, w io.Writer) error {
	if _, err := s.next(); err != nil {
		return err
	}
	var one [1]byte
	for {
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
