package insert

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

var errInvalidInsertModifier = errors.New("после OR ожидается REPLACE или IGNORE")

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
		case b == '$':
			if err := s.skipDollarQuoted(); err != nil {
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
		return s.skipUntilSemicolon(true, false)
	}

	if err := skipModifiers(s); err != nil {
		if errors.Is(err, errInvalidInsertModifier) {
			return skip(err.Error(), "")
		}
		return err
	}
	// INTO необязателен в MySQL и MSSQL (INSERT users VALUES …). Без INTO и без
	// идентификатора дальше (GRANT INSERT, UPDATE …) это не оператор данных.
	if ok, err := s.tryKeyword("INTO"); err != nil {
		return err
	} else if !ok {
		if err := s.skipSpaceAndComments(); err != nil {
			return err
		}
		if b, err := s.peek(); err != nil || !startsIdent(b) {
			return skip("нет INTO", "")
		}
	}

	table, err := parseTableName(s)
	if err != nil {
		if err == io.EOF {
			return skip("незакрытый INSERT", "")
		}
		return skip(err.Error(), "")
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

	var cols []string
	valuesSeen := false

	switch b {
	case '(':
		cols, err = parseColumnList(s)
		if err != nil {
			if err == io.EOF {
				return skip("незакрытый INSERT", table)
			}
			return skip(err.Error(), table)
		}
		if len(cols) == 0 {
			return skip("пустой список колонок", table)
		}
	default:
		if ok, err := s.tryKeyword("SET"); err != nil {
			return err
		} else if ok {
			return skip("INSERT ... SET", table)
		}
		if ok, err := s.tryKeyword("VALUES"); err != nil {
			return err
		} else if ok {
			valuesSeen = true
			break
		}
		if ok, err := s.tryKeyword("SELECT"); err != nil {
			return err
		} else if ok {
			return skip("INSERT ... SELECT", table)
		}
		return skip("нет VALUES", table)
	}

	if !valuesSeen {
		if err := s.skipTailSpaceAndComments(); err != nil {
			if errors.Is(err, errUnclosedBlockComment) {
				return skip(err.Error(), table)
			}
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
			return skip("нет VALUES", table)
		}
	}

	meta := Meta{Table: table, Columns: cols, Offset: start.Offset, Line: start.Line}
	if h.BeforeValues != nil {
		skipBody, err := h.BeforeValues(meta)
		if err != nil {
			return err
		}
		if skipBody {
			return s.skipUntilSemicolon(true, false)
		}
	}
	if h.ValuesAt != nil {
		off := s.pos
		if err := s.skipUntilSemicolon(true, false); err != nil {
			return err
		}
		return h.ValuesAt(meta, off, s.pos-off)
	}
	return parseValueRows(s, h, meta)
}

// ParseValues разбирает уже вырезанный хвост одного INSERT ... VALUES.
// Для io.SectionReader буфер не больше самого хвоста: однострочный INSERT
// не должен выделять полный буфер чтения.
func ParseValues(r io.Reader, meta Meta, h Handler) error {
	size := readBuf
	if sized, ok := r.(interface{ Size() int64 }); ok && sized.Size() < int64(size) {
		size = int(sized.Size())
	}
	return parseValueRows(newSrcSize(r, size), h, meta)
}

func parseValueRows(s *src, h Handler, meta Meta) error {
	table := meta.Table
	cols := meta.Columns
	skip := func(reason, table string) error {
		if h.Skip != nil {
			h.Skip(Skip{Offset: meta.Offset, Line: meta.Line, Table: table, Reason: reason})
		}
		return s.skipUntilSemicolon(true, false)
	}
	began := false
	accepted := 0
	expectRow := false
	maxCells := len(cols)

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

	noteSurplus := func() {
		if h.RowSurplus != nil {
			h.RowSurplus()
		}
	}

	for {
		if err := s.skipSpaceAndComments(); err != nil {
			return err
		}
		b, err := s.peek()
		if err == io.EOF {
			if expectRow {
				return skip("после запятой нет строки VALUES", table)
			}
			break
		}
		if err != nil {
			return err
		}
		if b != '(' {
			if expectRow {
				return skip("после запятой нет строки VALUES", table)
			}
			break
		}
		if h.StreamRow != nil {
			if err := begin(); err != nil {
				return err
			}
			err = h.StreamRow(func(cw CellWriter) error {
				return emitRow(s, cw, maxCells)
			})
		} else {
			var cells []Cell
			cells, err = parseRow(s, maxCells)
			if err == nil {
				if err := begin(); err != nil {
					return err
				}
				if h.Row != nil {
					// Ошибка Row, кроме ErrRowSurplus, — I/O потребителя: стоп разбора.
					if err = h.Row(cells); err != nil && !errors.Is(err, ErrRowSurplus) {
						return err
					}
				}
			}
		}
		switch {
		case errors.Is(err, ErrRowSurplus):
			noteSurplus()
		case err != nil:
			if began {
				_ = skip("битый INSERT: "+err.Error(), table)
				return nil
			}
			return skip("битый INSERT: "+err.Error(), table)
		default:
			accepted++
		}

		if err := s.skipTailSpaceAndComments(); err != nil {
			if errors.Is(err, errUnclosedBlockComment) {
				return skip(err.Error(), table)
			}
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
			expectRow = true
			continue
		}
		break
	}

	if accepted == 0 {
		// Все строки отброшены по ширине: Skip сбросит начатый temp.
		return skip("нет строк VALUES", table)
	}
	validTail, err := consumeTail(s)
	if err != nil {
		return err
	}
	if !validTail {
		return skip("неожиданный хвост INSERT", table)
	}
	if h.End != nil {
		return h.End()
	}
	return nil
}

func consumeTail(s *src) (bool, error) {
	if err := s.skipTailSpaceAndComments(); err != nil {
		if errors.Is(err, errUnclosedBlockComment) || errors.Is(err, errIncompleteInsertTail) {
			return false, nil
		}
		return false, err
	}
	b, err := s.peek()
	if err == io.EOF {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if b == ';' {
		_, _ = s.next()
		return true, nil
	}
	if ok, err := s.tryKeyword("ON"); err != nil {
		return false, err
	} else if ok {
		err := s.skipUntilSemicolon(false, true)
		if errors.Is(err, errUnclosedBlockComment) || errors.Is(err, errIncompleteInsertTail) {
			return false, nil
		}
		return true, err
	}
	if ok, err := s.tryKeyword("RETURNING"); err != nil {
		return false, err
	} else if ok {
		err := s.skipUntilSemicolon(false, true)
		if errors.Is(err, errUnclosedBlockComment) || errors.Is(err, errIncompleteInsertTail) {
			return false, nil
		}
		return true, err
	}
	return false, nil
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
			if replace, err := s.tryKeyword("REPLACE"); err != nil {
				return err
			} else if replace {
				continue
			}
			if ignore, err := s.tryKeyword("IGNORE"); err != nil {
				return err
			} else if ignore {
				continue
			}
			return errInvalidInsertModifier
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

// parseTableName берёт последний сегмент имени: table, schema.table и
// db.schema.table (MSSQL [shop].[dbo].[users]) дают одно имя таблицы.
func parseTableName(s *src) (string, error) {
	part, err := parseIdent(s)
	if err != nil {
		return "", err
	}
	for {
		if err := s.skipSpaceAndComments(); err != nil {
			return "", err
		}
		b, err := s.peek()
		if err == io.EOF || (err == nil && b != '.') {
			return cleanIdent(part), nil
		}
		if err != nil {
			return "", err
		}
		_, _ = s.next()
		if part, err = parseIdent(s); err != nil {
			return "", err
		}
	}
}

// startsIdent — байт может начинать имя таблицы: слово или обрамление.
func startsIdent(b byte) bool {
	return identStart(b) || b == '`' || b == '"' || b == '[' || b == '\''
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
	expectIdent := true
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
			if expectIdent && len(cols) > 0 {
				return nil, fmt.Errorf("запятая без колонки")
			}
			return cols, nil
		}
		if !expectIdent {
			if b != ',' {
				return nil, fmt.Errorf("битый список колонок")
			}
			_, _ = s.next()
			expectIdent = true
			continue
		}
		if b == ',' {
			return nil, fmt.Errorf("запятая без колонки")
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
		expectIdent = false
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

// parseRow собирает одну строку VALUES в []Cell для обработчика Row.
// Разбор тот же, что у потокового StreamRow: один код для обоих путей.
func parseRow(s *src, maxCells int) ([]Cell, error) {
	var c cellCollector
	err := emitRow(s, &c, maxCells)
	return c.cells, err
}

// cellCollector — CellWriter, который копит ячейки строки в память.
type cellCollector struct {
	cells []Cell
	b     strings.Builder
}

func (c *cellCollector) Null() error {
	c.cells = append(c.cells, Cell{Kind: Null})
	return nil
}

func (c *cellCollector) Missing() error {
	c.cells = append(c.cells, Cell{Kind: Missing})
	return nil
}

func (c *cellCollector) Text(write func(io.Writer) error) error {
	c.b.Reset()
	if err := write(&c.b); err != nil {
		return err
	}
	c.cells = append(c.cells, Cell{Kind: Text, Text: c.b.String()})
	return nil
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
		if quote != '`' && c == '\\' {
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
