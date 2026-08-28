package insert

import (
	"fmt"
	"io"
	"strings"
)

// Parse читает r потоково и вызывает Handler на каждый INSERT ... VALUES.
// Остальной SQL и отвергнутые INSERT не являются ошибкой: они уходят в Skip.
// Ошибка возвращается только при сбое чтения или ошибке колбэка.
func Parse(r io.Reader, h Handler) error {
	s := newSrc(r)
	s.skipBOM()
	for {
		found, err := seekInsert(s)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if !found {
			return nil
		}
		if err := parseInsert(s, h); err != nil {
			return err
		}
	}
}

func seekInsert(s *src) (bool, error) {
	for {
		b, err := s.peek()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		switch {
		case isSpace(b):
			if _, err := s.next(); err != nil {
				return false, err
			}
		case b == '-' && s.starts("--"):
			if err := s.skipLineComment(); err != nil {
				return false, err
			}
		case b == '/' && s.starts("/*"):
			if err := s.skipBlockComment(); err != nil {
				return false, err
			}
		case b == '\'', b == '"', b == '`':
			if err := s.skipQuoted(b); err != nil {
				if err == io.EOF {
					return false, nil
				}
				return false, err
			}
		case b == '[':
			if err := s.skipBracketIdent(); err != nil {
				if err == io.EOF {
					return false, nil
				}
				return false, err
			}
		case identStart(b):
			word, err := s.consumeUnquotedWord()
			if err != nil {
				return false, err
			}
			if strings.EqualFold(word, "INSERT") {
				return true, nil
			}
		default:
			if _, err := s.next(); err != nil {
				return false, err
			}
		}
	}
}

func parseInsert(s *src, h Handler) error {
	start := Skip{Offset: s.pos - int64(len("INSERT")), Line: s.line}
	skip := func(reason string, table string) error {
		if h.Skip != nil {
			h.Skip(Skip{Offset: start.Offset, Line: start.Line, Table: table, Reason: reason})
		}
		return s.skipUntilSemicolon(true)
	}

	if err := skipModifiers(s); err != nil {
		return err
	}
	if ok, err := s.tryKeyword("INTO"); err != nil {
		return err
	} else if !ok {
		return skip("нет INTO", "")
	}

	table, err := parseTableName(s)
	if err != nil {
		if err == io.EOF {
			return skip("незакрытый INSERT", "")
		}
		return err
	}
	if table == "" {
		return skip("пустое имя таблицы", "")
	}
	start.Table = table

	if err := s.skipSpaceAndComments(); err != nil {
		return err
	}
	if ok, err := s.tryKeyword("PARTITION"); err != nil {
		return err
	} else if ok {
		if err := skipParenGroup(s); err != nil {
			return skip("битый PARTITION", table)
		}
	}

	if err := s.skipSpaceAndComments(); err != nil {
		return err
	}
	b, err := s.peek()
	if err == io.EOF {
		return skip("незакрытый INSERT", table)
	}
	if err != nil {
		return err
	}

	switch {
	case b == '(':
		// список колонок — нормальный путь
	default:
		if ok, err := s.tryKeyword("SET"); err != nil {
			return err
		} else if ok {
			return skip("INSERT ... SET", table)
		}
		if ok, err := s.tryKeyword("VALUES"); err != nil {
			return err
		} else if ok {
			return skip("нет списка колонок", table)
		}
		if ok, err := s.tryKeyword("SELECT"); err != nil {
			return err
		} else if ok {
			return skip("INSERT ... SELECT", table)
		}
		return skip("нет списка колонок", table)
	}

	cols, err := parseColumnList(s)
	if err != nil {
		if err == io.EOF {
			return skip("незакрытый INSERT", table)
		}
		return skip(err.Error(), table)
	}
	if len(cols) == 0 {
		return skip("пустой список колонок", table)
	}

	if err := s.skipSpaceAndComments(); err != nil {
		return err
	}
	if ok, err := s.tryKeyword("SELECT"); err != nil {
		return err
	} else if ok {
		return skip("INSERT ... SELECT", table)
	}
	if ok, err := s.tryKeyword("VALUES"); err != nil {
		return err
	} else if !ok {
		if ok, err := s.tryKeyword("SET"); err != nil {
			return err
		} else if ok {
			return skip("INSERT ... SET", table)
		}
		if ok, err := s.tryKeyword("SELECT"); err != nil {
			return err
		} else if ok {
			return skip("INSERT ... SELECT", table)
		}
		return skip("нет VALUES", table)
	}

	meta := Meta{Table: table, Columns: cols, Offset: start.Offset, Line: start.Line}
	began := false
	surplus := false
	rows := 0

	begin := func() error {
		if began {
			return nil
		}
		began = true
		if h.Begin != nil {
			return h.Begin(meta)
		}
		return nil
	}

	for {
		if err := s.skipSpaceAndComments(); err != nil {
			return err
		}
		b, err := s.peek()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if b != '(' {
			break
		}
		cells, err := parseRow(s)
		if err != nil {
			if began {
				_ = skip("битый INSERT: "+err.Error(), table)
				return nil
			}
			return skip("битый INSERT: "+err.Error(), table)
		}
		if len(cells) > len(cols) {
			surplus = true
		}
		if !surplus {
			if err := begin(); err != nil {
				return err
			}
			if h.Row != nil {
				if err := h.Row(cells); err != nil {
					return err
				}
			}
		}
		rows++
		if err := s.skipSpaceAndComments(); err != nil {
			return err
		}
		b, err = s.peek()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if b == ',' {
			_, _ = s.next()
			continue
		}
		break
	}

	if surplus {
		return skip("значений больше, чем колонок", table)
	}
	if rows == 0 {
		return skip("нет строк VALUES", table)
	}
	if err := consumeTail(s); err != nil {
		return err
	}
	if h.End != nil {
		return h.End()
	}
	return nil
}

func consumeTail(s *src) error {
	if err := s.skipSpaceAndComments(); err != nil {
		return err
	}
	b, err := s.peek()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	if b == ';' {
		_, _ = s.next()
		return nil
	}
	if ok, err := s.tryKeyword("ON"); err != nil {
		return err
	} else if ok {
		return s.skipUntilSemicolon(false)
	}
	if ok, err := s.tryKeyword("RETURNING"); err != nil {
		return err
	} else if ok {
		return s.skipUntilSemicolon(false)
	}
	return nil
}

func skipModifiers(s *src) error {
	for {
		if err := s.skipSpaceAndComments(); err != nil {
			return err
		}
		ok, err := s.tryKeyword("OR")
		if err != nil {
			return err
		}
		if ok {
			if _, err := s.tryKeyword("REPLACE"); err != nil {
				return err
			}
			if _, err := s.tryKeyword("IGNORE"); err != nil {
				return err
			}
			continue
		}
		matched := false
		for _, kw := range []string{"IGNORE", "DELAYED", "LOW_PRIORITY", "HIGH_PRIORITY"} {
			ok, err := s.tryKeyword(kw)
			if err != nil {
				return err
			}
			if ok {
				matched = true
				break
			}
		}
		if !matched {
			return nil
		}
	}
}

func parseTableName(s *src) (string, error) {
	part, err := parseIdent(s)
	if err != nil {
		return "", err
	}
	if err := s.skipSpaceAndComments(); err != nil {
		return "", err
	}
	b, err := s.peek()
	if err != nil && err != io.EOF {
		return "", err
	}
	if err == nil && b == '.' {
		_, _ = s.next()
		next, err := parseIdent(s)
		if err != nil {
			return "", err
		}
		part = next
	}
	return cleanIdent(part), nil
}

func parseIdent(s *src) (string, error) {
	if err := s.skipSpaceAndComments(); err != nil {
		return "", err
	}
	b, err := s.peek()
	if err != nil {
		return "", err
	}
	switch b {
	case '`', '"', '\'':
		return readQuotedIdent(s, b)
	case '[':
		return readBracketIdent(s)
	default:
		if !identStart(b) {
			return "", fmt.Errorf("ожидался идентификатор")
		}
		return s.consumeUnquotedWord()
	}
}

func readQuotedIdent(s *src, quote byte) (string, error) {
	if _, err := s.next(); err != nil {
		return "", err
	}
	var b strings.Builder
	for {
		c, err := s.next()
		if err != nil {
			return "", err
		}
		if c == quote {
			n, err := s.peek()
			if err == nil && n == quote {
				_, _ = s.next()
				b.WriteByte(quote)
				continue
			}
			return b.String(), nil
		}
		b.WriteByte(c)
	}
}

func readBracketIdent(s *src) (string, error) {
	if _, err := s.next(); err != nil {
		return "", err
	}
	var b strings.Builder
	for {
		c, err := s.next()
		if err != nil {
			return "", err
		}
		if c == ']' {
			return b.String(), nil
		}
		b.WriteByte(c)
	}
}

func parseColumnList(s *src) ([]string, error) {
	if err := s.skipSpaceAndComments(); err != nil {
		return nil, err
	}
	b, err := s.peek()
	if err != nil {
		return nil, err
	}
	if b != '(' {
		return nil, fmt.Errorf("ожидалась скобка списка колонок")
	}
	_, _ = s.next()

	var cols []string
	for {
		if err := s.skipSpaceAndComments(); err != nil {
			return nil, err
		}
		b, err := s.peek()
		if err != nil {
			return nil, err
		}
		if b == ')' {
			_, _ = s.next()
			return cols, nil
		}
		if b == ',' {
			_, _ = s.next()
			continue
		}
		name, err := parseIdent(s)
		if err != nil {
			return nil, err
		}
		name = cleanIdent(name)
		if err := s.skipSpaceAndComments(); err != nil {
			return nil, err
		}
		// schema.col — берём последний сегмент
		if b, err := s.peek(); err == nil && b == '.' {
			_, _ = s.next()
			next, err := parseIdent(s)
			if err != nil {
				return nil, err
			}
			name = cleanIdent(next)
		}
		cols = append(cols, name)
		if err := s.skipSpaceAndComments(); err != nil {
			return nil, err
		}
		b, err = s.peek()
		if err != nil {
			return nil, err
		}
		switch b {
		case ',':
			_, _ = s.next()
		case ')':
			_, _ = s.next()
			return cols, nil
		default:
			return nil, fmt.Errorf("битый список колонок")
		}
	}
}

func skipParenGroup(s *src) error {
	if err := s.skipSpaceAndComments(); err != nil {
		return err
	}
	b, err := s.peek()
	if err != nil {
		return err
	}
	if b != '(' {
		return fmt.Errorf("ожидалась '('")
	}
	depth := 0
	for {
		c, err := s.peek()
		if err != nil {
			return err
		}
		switch c {
		case '\'', '"', '`':
			if err := s.skipQuoted(c); err != nil {
				return err
			}
		case '(':
			depth++
			_, _ = s.next()
		case ')':
			_, _ = s.next()
			depth--
			if depth == 0 {
				return nil
			}
		default:
			_, _ = s.next()
		}
	}
}

func parseRow(s *src) ([]Cell, error) {
	if err := s.skipSpaceAndComments(); err != nil {
		return nil, err
	}
	b, err := s.peek()
	if err != nil {
		return nil, err
	}
	if b != '(' {
		return nil, fmt.Errorf("ожидалась '(' строки VALUES")
	}
	_, _ = s.next()

	var cells []Cell
	expectValue := true
	for {
		if err := s.skipSpaceAndComments(); err != nil {
			return nil, err
		}
		b, err := s.peek()
		if err != nil {
			return nil, err
		}
		if b == ')' {
			if expectValue && len(cells) > 0 {
				cells = append(cells, Cell{Kind: Missing})
			}
			_, _ = s.next()
			return cells, nil
		}
		if b == ',' {
			if expectValue {
				cells = append(cells, Cell{Kind: Missing})
			}
			_, _ = s.next()
			expectValue = true
			continue
		}
		cell, err := parseValue(s)
		if err != nil {
			return nil, err
		}
		cells = append(cells, cell)
		expectValue = false
	}
}

func parseValue(s *src) (Cell, error) {
	if err := s.skipSpaceAndComments(); err != nil {
		return Cell{}, err
	}
	b, err := s.peek()
	if err != nil {
		return Cell{}, err
	}
	switch b {
	case '\'':
		text, err := readString(s, '\'')
		return Cell{Kind: Text, Text: text}, err
	case '"':
		text, err := readString(s, '"')
		return Cell{Kind: Text, Text: text}, err
	}

	word, ok, err := s.peekUnquotedWord()
	if err != nil {
		return Cell{}, err
	}
	if ok && strings.EqualFold(word, "NULL") {
		for range word {
			_, _ = s.next()
		}
		return Cell{Kind: Null}, nil
	}

	text, err := readRawValue(s)
	if err != nil {
		return Cell{}, err
	}
	if text == "" {
		return Cell{Kind: Missing}, nil
	}
	return Cell{Kind: Text, Text: text}, nil
}

func readString(s *src, quote byte) (string, error) {
	if _, err := s.next(); err != nil {
		return "", err
	}
	var b strings.Builder
	for {
		c, err := s.next()
		if err != nil {
			return "", err
		}
		if c == '\\' {
			n, err := s.peek()
			if err == io.EOF {
				b.WriteByte('\\')
				return b.String(), nil
			}
			if err != nil {
				return "", err
			}
			_, _ = s.next()
			if n == quote || n == '\\' {
				b.WriteByte(n)
				continue
			}
			b.WriteByte('\\')
			b.WriteByte(n)
			continue
		}
		if c == quote {
			n, err := s.peek()
			if err == nil && n == quote {
				_, _ = s.next()
				b.WriteByte(quote)
				continue
			}
			return b.String(), nil
		}
		b.WriteByte(c)
	}
}

func readRawValue(s *src) (string, error) {
	var b strings.Builder
	depth := 0
	for {
		c, err := s.peek()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if depth == 0 && (c == ',' || c == ')') {
			break
		}
		if c == '-' && s.starts("--") && depth == 0 {
			break
		}
		if c == '/' && s.starts("/*") {
			if err := s.skipBlockComment(); err != nil {
				return "", err
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			chunk, err := readQuotedRaw(s, c)
			if err != nil {
				return "", err
			}
			b.WriteString(chunk)
		case '(':
			depth++
			_, _ = s.next()
			b.WriteByte('(')
		case ')':
			if depth == 0 {
				return strings.TrimSpace(b.String()), nil
			}
			depth--
			_, _ = s.next()
			b.WriteByte(')')
		default:
			_, _ = s.next()
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func readQuotedRaw(s *src, quote byte) (string, error) {
	var b strings.Builder
	open, err := s.next()
	if err != nil {
		return "", err
	}
	b.WriteByte(open)
	for {
		c, err := s.next()
		if err != nil {
			return "", err
		}
		b.WriteByte(c)
		if quote == '\'' && c == '\\' {
			n, err := s.next()
			if err != nil {
				return b.String(), err
			}
			b.WriteByte(n)
			continue
		}
		if c != quote {
			continue
		}
		n, err := s.peek()
		if err == nil && n == quote {
			_, _ = s.next()
			b.WriteByte(n)
			continue
		}
		return b.String(), nil
	}
}
