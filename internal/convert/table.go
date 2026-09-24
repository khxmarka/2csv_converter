package convert

import (
	"bufio"
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"sql2csv/internal/csvout"
	"sql2csv/internal/logx"
	"sql2csv/internal/pii"
	"sql2csv/internal/scan"
)

const tableReaderSize = 1024 * 1024

var tableDelims = []rune{',', ';', '\t'}

var sqlStartWords = map[string]struct{}{
	"INSERT":   {},
	"CREATE":   {},
	"UPDATE":   {},
	"DELETE":   {},
	"WITH":     {},
	"BEGIN":    {},
	"ALTER":    {},
	"DROP":     {},
	"REPLACE":  {},
	"TRUNCATE": {},
	"SELECT":   {},
	"SET":      {},
	"CALL":     {},
	"GRANT":    {},
	"REVOKE":   {},
	"LOAD":     {},
	"EXEC":     {},
	"EXECUTE":  {},
	"MERGE":    {},
	"USE":      {},
	// START/COMMIT/ROLLBACK сюда не входят: «Start Date» — обычная шапка,
	// а «COMMIT;» отсекает pickTableHeader по пустому хвосту после ';'.
	"PRAGMA":   {},
	"DECLARE":  {},
	"IF":       {},
	"COPY":     {},
	"PRINT":    {},
	"SHOW":     {},
	"EXPLAIN":  {},
	"DESCRIBE": {},
	"DESC":     {},
	"LOCK":     {},
	"UNLOCK":   {},
	"DO":       {},
}

var insertPrefixWords = map[string]struct{}{
	"IGNORE":        {},
	"DELAYED":       {},
	"LOW_PRIORITY":  {},
	"HIGH_PRIORITY": {},
	"OR":            {},
}

type tableHeader struct {
	delim   rune
	columns []string
}

func sniffTable(f *os.File) (*tableHeader, *bufio.Reader, error) {
	r := bufio.NewReaderSize(f, tableReaderSize)
	if err := skipReaderBOM(r); err != nil {
		if err == io.EOF {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	for {
		// ReadSlice, а не ReadString: однострочный дамп на гигабайты не должен
		// попасть в память целиком (§2). Шапка длиннее буфера — это не шапка.
		raw, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			return nil, nil, nil
		}
		if err != nil && err != io.EOF {
			return nil, nil, err
		}
		line := trimLine(string(raw))
		if strings.TrimSpace(line) != "" {
			if looksLikeSQL(line) {
				return nil, nil, nil
			}
			h, ok := pickTableHeader(line)
			if !ok {
				return nil, nil, nil
			}
			return h, r, nil
		}
		if err == io.EOF {
			return nil, nil, nil
		}
	}
}

func skipReaderBOM(r *bufio.Reader) error {
	head, err := r.Peek(3)
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	if len(head) >= 3 && head[0] == 0xEF && head[1] == 0xBB && head[2] == 0xBF {
		_, err = r.Discard(3)
		return err
	}
	return nil
}

func trimLine(s string) string {
	s = strings.TrimSuffix(s, "\n")
	return strings.TrimSuffix(s, "\r")
}

func looksLikeSQL(line string) bool {
	s := strings.TrimSpace(line)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "--") || strings.HasPrefix(s, "/*") {
		return true
	}
	for {
		word, rest, ok := nextWord(s)
		if !ok {
			return false
		}
		// Слово перед запятой — имя колонки шапки (desc,email), не оператор.
		if strings.HasPrefix(rest, ",") {
			return false
		}
		upper := strings.ToUpper(word)
		if _, sql := sqlStartWords[upper]; sql {
			return true
		}
		if _, prefix := insertPrefixWords[upper]; prefix {
			s = rest
			continue
		}
		return false
	}
}

func nextWord(s string) (word, rest string, ok bool) {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	if s == "" {
		return "", "", false
	}
	i := 0
	for _, r := range s {
		if unicode.IsSpace(r) || r == '(' || r == ';' || r == ',' {
			break
		}
		i += utf8.RuneLen(r)
	}
	if i == 0 {
		return "", s, false
	}
	return s[:i], s[i:], true
}

// pickTableHeader считает только непустые поля: у «PRAGMA x=1;» после ';'
// пустой хвост, это не вторая колонка. Пустое имя внутри шапки (индекс pandas)
// остаётся колонкой.
func pickTableHeader(line string) (*tableHeader, bool) {
	bestN := 1
	var best *tableHeader
	for _, d := range tableDelims {
		fields, err := splitFields(line, d)
		if err != nil {
			continue
		}
		n := nonEmptyFields(fields)
		if n <= bestN {
			continue
		}
		bestN = n
		cols := make([]string, len(fields))
		for i, f := range fields {
			cols[i] = strings.TrimSpace(f)
		}
		best = &tableHeader{delim: d, columns: cols}
	}
	if best == nil || bestN < 2 {
		return nil, false
	}
	return best, true
}

func nonEmptyFields(fields []string) int {
	n := 0
	for _, f := range fields {
		if strings.TrimSpace(f) != "" {
			n++
		}
	}
	return n
}

func splitFields(line string, delim rune) ([]string, error) {
	cr := csv.NewReader(strings.NewReader(line))
	cr.Comma = delim
	cr.FieldsPerRecord = -1
	rec, err := cr.Read()
	if err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, len(rec))
	copy(out, rec)
	return out, nil
}

// maxTableRecord — предел склейки строк с незакрытой кавычкой. Поле в кавычках
// может содержать перевод строки, но одна битая кавычка не должна съесть файл.
const maxTableRecord = 1 << 20

// tableReader отдаёт записи табличного .sql без перевода строки в конце.
// Строка с нечётным числом кавычек склеивается со следующими, пока кавычки
// не закроются. Не закрылись до maxTableRecord или EOF — запись только из
// первой строки (она битая), прочитанные вперёд строки идут в разбор снова.
type tableReader struct {
	r       *bufio.Reader
	pending []string
	// noJoin — сколько строк из pending отдать без склейки: они уже были
	// хвостом неудачной склейки. Без этого файл, где кавычка нечётна в каждой
	// строке, читался бы вперёд на maxTableRecord от каждой строки.
	noJoin int
	eof    bool
}

func (t *tableReader) line() (string, bool, error) {
	if len(t.pending) > 0 {
		l := t.pending[0]
		t.pending = t.pending[1:]
		return l, true, nil
	}
	if t.eof {
		return "", false, nil
	}
	l, err := t.r.ReadString('\n')
	if err == io.EOF {
		t.eof = true
		return l, l != "", nil
	}
	if err != nil {
		return "", false, err
	}
	return l, true, nil
}

func (t *tableReader) next() (string, bool, error) {
	first, ok, err := t.line()
	if err != nil || !ok {
		return "", ok, err
	}
	if t.noJoin > 0 {
		t.noJoin--
		return trimLine(first), true, nil
	}
	quotes := strings.Count(first, `"`)
	if quotes%2 == 0 {
		return trimLine(first), true, nil
	}
	group := []string{first}
	size := len(first)
	for quotes%2 != 0 && size < maxTableRecord {
		l, ok, err := t.line()
		if err != nil {
			return "", false, err
		}
		if !ok {
			break
		}
		group = append(group, l)
		size += len(l)
		quotes += strings.Count(l, `"`)
	}
	if quotes%2 == 0 {
		return trimLine(strings.Join(group, "")), true, nil
	}
	t.pending = append(group[1:], t.pending...)
	t.noJoin = len(group) - 1
	return trimLine(first), true, nil
}

func fileStem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func convertTable(log *logx.Logger, reg *csvout.Registry, sql scan.SQLFile, h *tableHeader, r *bufio.Reader) Result {
	log.Warnf("%s: не INSERT, а таблица", sql.Path)
	stem := fileStem(sql.Path)
	if !pii.Match(stem) && !pii.MatchColumns(h.columns) {
		return Result{Skipped: 1, PIISkip: 1}
	}
	w, err := csvout.Create(reg, filepath.Dir(sql.Path), stem, h.columns)
	if err != nil {
		log.Errorf("%s таблица %s: не удалось создать CSV: %v", sql.Path, stem, err)
		return Result{Skipped: 1, Failed: true}
	}
	out := Result{}
	wrote, badParse, tooMany := 0, 0, 0
	tr := tableReader{r: r}
	for {
		rec, ok, err := tr.next()
		if err != nil {
			_ = w.Abort()
			log.Errorf("%s таблица %s: %v", sql.Path, stem, err)
			return Result{Skipped: 1, Failed: true, UnitFail: 1}
		}
		if !ok {
			break
		}
		if strings.TrimSpace(rec) == "" {
			continue
		}
		fields, splitErr := splitFields(rec, h.delim)
		if splitErr != nil {
			badParse++
			continue
		}
		if err := w.Row(fields); err != nil {
			if err == csvout.ErrTooManyValues {
				tooMany++
				continue
			}
			_ = w.Abort()
			log.Errorf("%s таблица %s: запись CSV: %v", sql.Path, stem, err)
			return Result{Skipped: badParse + tooMany + 1, Failed: true, UnitFail: badParse + tooMany + 1}
		}
		wrote++
	}
	// Одна строка на файл, а не на каждую битую строку: иначе большой
	// табличный дамп затапливает консоль (§8).
	if bad := badParse + tooMany; bad > 0 {
		out.Skipped += bad
		out.UnitFail += bad
		log.Errorf("%s таблица %s: пропущено строк: %d (не разобрать: %d, значений больше, чем колонок: %d)",
			sql.Path, stem, bad, badParse, tooMany)
	}
	if wrote == 0 && out.UnitFail > 0 {
		_ = w.Abort()
		return Result{Skipped: out.Skipped, UnitFail: out.UnitFail}
	}
	res, err := w.Commit()
	if err != nil {
		log.Errorf("%s таблица %s: не удалось записать CSV: %v", sql.Path, stem, err)
		return Result{Skipped: out.Skipped + 1, Failed: true, UnitFail: out.UnitFail}
	}
	out.Paths = []string{res.Path}
	if !res.Appended {
		out.CSV = 1
	}
	return out
}
