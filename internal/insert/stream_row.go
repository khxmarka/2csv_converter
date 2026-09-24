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
		return cw.Text(func(w io.Writer) error {
			return streamStringContent(s, b, w)
		})
	case 'N', 'n', 'E', 'e':
		// N'…' (MSSQL, Unicode) и E'…' (Postgres) — тот же строковый литерал,
		// префикс в ячейку не идёт. NOW(), NULL и прочие слова — не литерал.
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

	// Сырые значения (числа, выражения) короткие — буфер допустим.
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

// discardCells — CellWriter для лишних значений строки: разбирает и выбрасывает,
// не держа текст в памяти.
type discardCells struct{}

func (discardCells) Null() error    { return nil }
func (discardCells) Missing() error { return nil }
func (discardCells) Text(write func(io.Writer) error) error {
	return write(io.Discard)
}

func skipValue(s *src) error {
	return emitValue(s, discardCells{})
}

// streamStringContent читает SQL-строку (открывающая кавычка ещё не съедена)
// и пишет декодированное содержимое в w.
func streamStringContent(s *src, quote byte, w io.Writer) error {
	if _, err := s.next(); err != nil {
		return err
	}
	var one [1]byte
	for {
		// Обычный текст до кавычки или '\\' копируется из буфера одним куском:
		// побайтовая запись через writer-цепочку в разы медленнее.
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
