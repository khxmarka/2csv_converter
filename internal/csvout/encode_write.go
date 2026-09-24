package csvout

import (
	"io"
	"unicode/utf8"
)

// WriteNullField пишет пустую CSV-ячейку "".
func WriteNullField(w io.Writer) error {
	_, err := io.WriteString(w, `""`)
	return err
}

// WriteField пишет одно CSV-поле в кавычках с экранированием ".
func WriteField(w io.Writer, s string) error {
	if _, err := w.Write([]byte{'"'}); err != nil {
		return err
	}
	if err := writeEscapedW(w, s); err != nil {
		return err
	}
	_, err := w.Write([]byte{'"'})
	return err
}

// WriteFieldStream пишет CSV-поле, копируя сырой текст через write в кавычки.
// write получает writer, который экранирует " на лету.
func WriteFieldStream(w io.Writer, write func(io.Writer) error) error {
	if _, err := w.Write([]byte{'"'}); err != nil {
		return err
	}
	if err := write(&escapeWriter{w: w}); err != nil {
		return err
	}
	_, err := w.Write([]byte{'"'})
	return err
}

type escapeWriter struct {
	w io.Writer
}

func (e *escapeWriter) Write(p []byte) (int, error) {
	n := 0
	for i := 0; i < len(p); {
		r, size := utf8.DecodeRune(p[i:])
		if r == utf8.RuneError && size == 1 {
			if _, err := e.w.Write(p[i : i+1]); err != nil {
				return n, err
			}
			i++
			n++
			continue
		}
		if r == '"' {
			if _, err := io.WriteString(e.w, `""`); err != nil {
				return n, err
			}
		} else {
			if _, err := e.w.Write(p[i : i+size]); err != nil {
				return n, err
			}
		}
		i += size
		n += size
	}
	return n, nil
}

func writeEscapedW(w io.Writer, s string) error {
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == '"' {
			if _, err := io.WriteString(w, `""`); err != nil {
				return err
			}
		} else {
			if _, err := io.WriteString(w, s[i:i+n]); err != nil {
				return err
			}
		}
		i += n
	}
	return nil
}
