package insert

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type collected struct {
	meta Meta
	rows [][]Cell
}

func collect(t *testing.T, sql string) (inserts []collected, skips []Skip) {
	t.Helper()
	var cur *collected
	err := Parse(strings.NewReader(sql), Handler{
		Begin: func(m Meta) error {
			c := collected{meta: m}
			cur = &c
			return nil
		},
		Row: func(cells []Cell) error {
			if cur == nil {
				t.Fatal("Row без Begin")
			}
			cloned := make([]Cell, len(cells))
			copy(cloned, cells)
			cur.rows = append(cur.rows, cloned)
			return nil
		},
		End: func() error {
			if cur == nil {
				t.Fatal("End без Begin")
			}
			inserts = append(inserts, *cur)
			cur = nil
			return nil
		},
		Skip: func(s Skip) {
			if cur != nil {
				cur = nil
			}
			skips = append(skips, s)
		},
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cur != nil {
		t.Fatal("незакрытый Begin без End и без Skip")
	}
	return inserts, skips
}

func TestParseSingleValues(t *testing.T) {
	inserts, skips := collect(t, `INSERT INTO users (id, name) VALUES (1, 'Ann');`)
	if len(skips) != 0 {
		t.Fatalf("пропусков не ожидалось: %+v", skips)
	}
	if len(inserts) != 1 {
		t.Fatalf("ожидался 1 INSERT, получено %d", len(inserts))
	}
	got := inserts[0]
	if got.meta.Table != "users" {
		t.Fatalf("таблица: %q", got.meta.Table)
	}
	if strings.Join(got.meta.Columns, ",") != "id,name" {
		t.Fatalf("колонки: %v", got.meta.Columns)
	}
	if len(got.rows) != 1 || got.rows[0][0].Text != "1" || got.rows[0][1].Text != "Ann" {
		t.Fatalf("строки: %+v", got.rows)
	}
}

func TestParseMultiValues(t *testing.T) {
	sql := `
INSERT INTO t (a, b) VALUES
	(1, 'x'),
	(2, 'y'),
	(3, 'z');
`
	inserts, skips := collect(t, sql)
	if len(skips) != 0 || len(inserts) != 1 {
		t.Fatalf("inserts=%d skips=%+v", len(inserts), skips)
	}
	if len(inserts[0].rows) != 3 {
		t.Fatalf("строк: %d", len(inserts[0].rows))
	}
	if inserts[0].rows[2][1].Text != "z" {
		t.Fatalf("третья строка: %+v", inserts[0].rows[2])
	}
}

func TestParseEscapedQuotes(t *testing.T) {
	inserts, skips := collect(t, `INSERT INTO t (s) VALUES ('it''s'), ('a\\b'), ('q\'q');`)
	if len(skips) != 0 || len(inserts) != 1 {
		t.Fatalf("inserts=%d skips=%+v", len(inserts), skips)
	}
	rows := inserts[0].rows
	want := []string{"it's", `a\b`, "q'q"}
	if len(rows) != 3 {
		t.Fatalf("строк: %d %+v", len(rows), rows)
	}
	for i, w := range want {
		if rows[i][0].Kind != Text || rows[i][0].Text != w {
			t.Fatalf("row %d: %+v, ожидалось %q", i, rows[i][0], w)
		}
	}
}

func TestParseBackslashNStaysTwoChars(t *testing.T) {
	inserts, _ := collect(t, `INSERT INTO t (s) VALUES ('a\nb');`)
	if inserts[0].rows[0][0].Text != `a\nb` {
		t.Fatalf("\\n не должен становиться переводом строки: %q", inserts[0].rows[0][0].Text)
	}
}

func TestParseBackticksAndSchema(t *testing.T) {
	sql := "INSERT INTO `db`.`users` (`id`, `first/name`) VALUES (1, 'a');"
	inserts, skips := collect(t, sql)
	if len(skips) != 0 {
		t.Fatalf("skips: %+v", skips)
	}
	if inserts[0].meta.Table != "users" {
		t.Fatalf("таблица без схемы: %q", inserts[0].meta.Table)
	}
	if inserts[0].meta.Columns[1] != "firstname" {
		t.Fatalf("мусор в имени колонки должен быть снят: %v", inserts[0].meta.Columns)
	}
}

func TestParseBracketAndQuotedIdents(t *testing.T) {
	sql := `INSERT INTO [dbo].[t] ("c1", [c2]) VALUES (1, 2);`
	inserts, skips := collect(t, sql)
	if len(skips) != 0 {
		t.Fatalf("skips: %+v", skips)
	}
	if inserts[0].meta.Table != "t" {
		t.Fatalf("таблица: %q", inserts[0].meta.Table)
	}
	if strings.Join(inserts[0].meta.Columns, ",") != "c1,c2" {
		t.Fatalf("колонки: %v", inserts[0].meta.Columns)
	}
}

func TestParseCommentsDoNotBreakSearch(t *testing.T) {
	sql := `
-- INSERT INTO fake (id) VALUES (0);
CREATE TABLE t (id INT);
/* INSERT INTO fake (id) VALUES (1); */
/*!40101 INSERT INTO fake (id) VALUES (2); */
INSERT INTO t -- comment
(id /* c */, name) VALUES (1, 'ok');
`
	inserts, skips := collect(t, sql)
	if len(skips) != 0 {
		t.Fatalf("skips: %+v", skips)
	}
	if len(inserts) != 1 || inserts[0].meta.Table != "t" || inserts[0].rows[0][1].Text != "ok" {
		t.Fatalf("ожидался один реальный INSERT: %+v", inserts)
	}
}

func TestParseStringLookingLikeInsert(t *testing.T) {
	sql := `INSERT INTO t (s) VALUES ('INSERT INTO x (id) VALUES (9)');`
	inserts, skips := collect(t, sql)
	if len(skips) != 0 || len(inserts) != 1 {
		t.Fatalf("inserts=%d skips=%+v", len(inserts), skips)
	}
	if inserts[0].rows[0][0].Text != "INSERT INTO x (id) VALUES (9)" {
		t.Fatalf("строка должна остаться литералом: %q", inserts[0].rows[0][0].Text)
	}
}

func TestParseSeveralInsertsInOneFile(t *testing.T) {
	sql := `
INSERT INTO a (id) VALUES (1);
UPDATE a SET id=2;
INSERT INTO b (id, n) VALUES (2, 'two'), (3, 'three');
DELETE FROM a;
INSERT IGNORE INTO c (x) VALUES (NULL);
`
	inserts, skips := collect(t, sql)
	if len(skips) != 0 {
		t.Fatalf("skips: %+v", skips)
	}
	if len(inserts) != 3 {
		t.Fatalf("ожидалось 3 INSERT, получено %d", len(inserts))
	}
	if inserts[0].meta.Table != "a" || inserts[1].meta.Table != "b" || inserts[2].meta.Table != "c" {
		t.Fatalf("таблицы: %q %q %q", inserts[0].meta.Table, inserts[1].meta.Table, inserts[2].meta.Table)
	}
	if len(inserts[1].rows) != 2 {
		t.Fatalf("второй INSERT должен иметь 2 строки")
	}
	if inserts[2].rows[0][0].Kind != Null {
		t.Fatalf("NULL: %+v", inserts[2].rows[0][0])
	}
}

func TestParseSkipSelectSet(t *testing.T) {
	sql := `
INSERT INTO t SELECT * FROM u;
INSERT INTO t SET a=1;
INSERT INTO t VALUES (1);
INSERT INTO ok (id) VALUES (1);
`
	inserts, skips := collect(t, sql)
	if len(inserts) != 2 {
		t.Fatalf("успешных: %+v", inserts)
	}
	if inserts[0].meta.Table != "t" || len(inserts[0].meta.Columns) != 0 {
		t.Fatalf("VALUES без колонок: %+v", inserts[0].meta)
	}
	if inserts[1].meta.Table != "ok" {
		t.Fatalf("второй INSERT: %+v", inserts[1].meta)
	}
	if len(skips) != 2 {
		t.Fatalf("ожидалось 2 пропуска, получено %d: %+v", len(skips), skips)
	}
	reasons := skips[0].Reason + skips[1].Reason
	for _, want := range []string{"SELECT", "SET"} {
		if !strings.Contains(reasons, want) {
			t.Fatalf("в причинах нет %q: %+v", want, skips)
		}
	}
}

func TestParseValuesWithoutColumns(t *testing.T) {
	sql := `INSERT INTO dle_xfsearch VALUES (334,10,'genre','Action'),(335,10,'genre','Adventure'),(336,10,'genre','Fantastique'),(337,10,'genre','Mystère')`
	inserts, skips := collect(t, sql)
	if len(skips) != 0 {
		t.Fatalf("пропусков не ожидалось: %+v", skips)
	}
	if len(inserts) != 1 {
		t.Fatalf("ожидался 1 INSERT, получено %d", len(inserts))
	}
	got := inserts[0]
	if got.meta.Table != "dle_xfsearch" {
		t.Fatalf("таблица: %q", got.meta.Table)
	}
	if len(got.meta.Columns) != 0 {
		t.Fatalf("колонки должны быть пусты: %v", got.meta.Columns)
	}
	if len(got.rows) != 4 {
		t.Fatalf("строк: %d, %+v", len(got.rows), got.rows)
	}
	if got.rows[0][0].Text != "334" || got.rows[0][3].Text != "Action" {
		t.Fatalf("первая строка: %+v", got.rows[0])
	}
	if got.rows[3][3].Text != "Mystère" {
		t.Fatalf("последняя строка: %+v", got.rows[3])
	}
}

func TestParseSkipEmptyValues(t *testing.T) {
	sql := `INSERT INTO t (id) VALUES; INSERT INTO u (id) VALUES (1);`
	inserts, skips := collect(t, sql)
	if len(skips) != 1 || skips[0].Reason != "нет строк VALUES" {
		t.Fatalf("skips: %+v", skips)
	}
	if len(inserts) != 1 || inserts[0].meta.Table != "u" {
		t.Fatalf("второй INSERT должен пройти: %+v", inserts)
	}
}

func TestParseRejectsMalformedSeparatorsAndContinues(t *testing.T) {
	tests := map[string]string{
		"values without comma": `INSERT INTO bad (email, name) VALUES ('a' 'Ann');
			INSERT INTO ok (email) VALUES ('ok@example.test');`,
		"rows without comma": `INSERT INTO bad (email) VALUES ('a') ('b');
			INSERT INTO ok (email) VALUES ('ok@example.test');`,
		"unexpected tail": `INSERT INTO bad (email) VALUES ('a') GARBAGE;
			INSERT INTO ok (email) VALUES ('ok@example.test');`,
	}
	for name, sql := range tests {
		t.Run(name, func(t *testing.T) {
			inserts, skips := collect(t, sql)
			if len(skips) != 1 || skips[0].Table != "bad" {
				t.Fatalf("ожидался пропуск bad: %+v", skips)
			}
			if len(inserts) != 1 || inserts[0].meta.Table != "ok" {
				t.Fatalf("следующий INSERT должен пройти: %+v", inserts)
			}
		})
	}
}

func TestParseRejectsMalformedColumnListsAndContinues(t *testing.T) {
	lists := []string{
		"(, email)",
		"(email,)",
		"(email,,name)",
		"(email name)",
	}
	for _, columns := range lists {
		t.Run(columns, func(t *testing.T) {
			sql := "INSERT INTO bad " + columns + " VALUES ('a');" +
				"INSERT INTO ok (email) VALUES ('ok@example.test');"
			inserts, skips := collect(t, sql)
			if len(skips) != 1 || skips[0].Table != "bad" {
				t.Fatalf("ожидался пропуск bad: %+v", skips)
			}
			if len(inserts) != 1 || inserts[0].meta.Table != "ok" {
				t.Fatalf("следующий INSERT должен пройти: %+v", inserts)
			}
		})
	}
}

func TestParseRejectsMalformedTableAndContinues(t *testing.T) {
	sql := `INSERT INTO 00;
		INSERT INTO ok (email) VALUES ('ok@example.test');`
	inserts, skips := collect(t, sql)
	if len(skips) != 1 {
		t.Fatalf("ожидался один пропуск: %+v", skips)
	}
	if len(inserts) != 1 || inserts[0].meta.Table != "ok" {
		t.Fatalf("следующий INSERT должен пройти: %+v", inserts)
	}
}

func TestParseRejectsInvalidORModifierAndContinues(t *testing.T) {
	sql := `INSERT OR INTO bad (email) VALUES ('bad@example.test');
		INSERT INTO ok (email) VALUES ('ok@example.test');`
	inserts, skips := collect(t, sql)
	if len(skips) != 1 {
		t.Fatalf("ожидался один пропуск: %+v", skips)
	}
	if len(inserts) != 1 || inserts[0].meta.Table != "ok" {
		t.Fatalf("следующий INSERT должен пройти: %+v", inserts)
	}
}

func TestParseRejectsUnclosedCommentAfterValues(t *testing.T) {
	tests := []string{
		`INSERT INTO users (email) VALUES ('a@example.test') /* unclosed`,
		`INSERT INTO users (email) VALUES ('a@example.test') ON DUPLICATE KEY UPDATE email='b' /* unclosed`,
	}
	for _, sql := range tests {
		inserts, skips := collect(t, sql)
		if len(inserts) != 0 || len(skips) != 1 {
			t.Fatalf("sql=%q inserts=%+v skips=%+v", sql, inserts, skips)
		}
	}
}

func TestParseRejectsIncompleteOnAndReturningTails(t *testing.T) {
	tests := []string{
		`INSERT INTO users (email) VALUES ('a@example.test') ON`,
		`INSERT INTO users (email) VALUES ('a@example.test') RETURNING;`,
		`INSERT INTO users (email) VALUES ('a@example.test') ON DUPLICATE KEY UPDATE email='unclosed`,
		`INSERT INTO users (email) VALUES ('a@example.test') RETURNING (email`,
	}
	for _, sql := range tests {
		inserts, skips := collect(t, sql)
		if len(inserts) != 0 || len(skips) != 1 {
			t.Fatalf("sql=%q inserts=%+v skips=%+v", sql, inserts, skips)
		}
	}
}

func TestParseUnclosedInsert(t *testing.T) {
	sql := `INSERT INTO t (id) VALUES (1, 'no-end`
	inserts, skips := collect(t, sql)
	if len(inserts) != 0 {
		t.Fatalf("незакрытый не должен коммититься: %+v", inserts)
	}
	if len(skips) != 1 {
		t.Fatalf("ожидался Skip: %+v", skips)
	}
}

func TestParseMissingValuesPaddedAsMissing(t *testing.T) {
	sql := `INSERT INTO t (a, b, c) VALUES (1), (2, 'x'), (3, , 'z');`
	inserts, skips := collect(t, sql)
	if len(skips) != 0 || len(inserts) != 1 {
		t.Fatalf("inserts=%d skips=%+v", len(inserts), skips)
	}
	rows := inserts[0].rows
	if len(rows[0]) != 1 || rows[0][0].Text != "1" {
		t.Fatalf("короткая строка должна дойти как есть: %+v", rows[0])
	}
	if len(rows[2]) != 3 || rows[2][1].Kind != Missing {
		t.Fatalf("дыра в середине: %+v", rows[2])
	}
}

func TestParseSurplusValuesKeepsValidRows(t *testing.T) {
	sql := `INSERT INTO t (a, b) VALUES (1, 2), (3, 4, 5), (6, 7); INSERT INTO u (a) VALUES (9);`
	var surplus int
	var inserts []collected
	var skips []Skip
	var cur *collected
	err := Parse(strings.NewReader(sql), Handler{
		Begin: func(m Meta) error {
			c := collected{meta: m}
			cur = &c
			return nil
		},
		Row: func(cells []Cell) error {
			if cur == nil {
				t.Fatal("Row без Begin")
			}
			cloned := make([]Cell, len(cells))
			copy(cloned, cells)
			cur.rows = append(cur.rows, cloned)
			return nil
		},
		RowSurplus: func() { surplus++ },
		End: func() error {
			inserts = append(inserts, *cur)
			cur = nil
			return nil
		},
		Skip: func(sk Skip) {
			cur = nil
			skips = append(skips, sk)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if surplus != 1 {
		t.Fatalf("surplus=%d", surplus)
	}
	if len(skips) != 0 {
		t.Fatalf("surplus не должен отменять INSERT: %+v", skips)
	}
	if len(inserts) != 2 || len(inserts[0].rows) != 2 {
		t.Fatalf("ожидались 2 валидные строки у t: %+v", inserts)
	}
	if inserts[0].rows[0][0].Text != "1" || inserts[0].rows[1][0].Text != "6" {
		t.Fatalf("строки: %+v", inserts[0].rows)
	}
	if inserts[1].meta.Table != "u" {
		t.Fatalf("второй INSERT: %+v", inserts[1])
	}
}

func TestParseAllSurplusSkipsInsert(t *testing.T) {
	sql := `INSERT INTO t (a, b) VALUES (1, 2, 3), (4, 5, 6); INSERT INTO u (a) VALUES (9);`
	inserts, skips := collect(t, sql)
	if len(inserts) != 1 || inserts[0].meta.Table != "u" {
		t.Fatalf("inserts=%+v", inserts)
	}
	if len(skips) != 1 || skips[0].Table != "t" {
		t.Fatalf("весь INSERT без валидных строк: %+v", skips)
	}
	if !strings.Contains(skips[0].Reason, "нет строк") {
		t.Fatalf("причина: %q", skips[0].Reason)
	}
}

func TestParseHexAndNumberAsText(t *testing.T) {
	sql := `INSERT INTO t (a, b, c) VALUES (0xFF, X'AB', 3.14);`
	inserts, skips := collect(t, sql)
	if len(skips) != 0 {
		t.Fatalf("skips: %+v", skips)
	}
	row := inserts[0].rows[0]
	if row[0].Text != "0xFF" || row[1].Text != "X'AB'" || row[2].Text != "3.14" {
		t.Fatalf("ячейки: %+v", row)
	}
}

func TestParseDoubleQuotedValue(t *testing.T) {
	sql := `INSERT INTO t (s) VALUES ("he""llo");`
	inserts, _ := collect(t, sql)
	if inserts[0].rows[0][0].Text != `he"llo` {
		t.Fatalf("got %q", inserts[0].rows[0][0].Text)
	}
}

func TestParseORReplaceAndDelayed(t *testing.T) {
	sql := `INSERT OR REPLACE INTO t (id) VALUES (1); INSERT DELAYED INTO u (id) VALUES (2);`
	inserts, skips := collect(t, sql)
	if len(skips) != 0 || len(inserts) != 2 {
		t.Fatalf("inserts=%d skips=%+v", len(inserts), skips)
	}
}

func TestParseBOM(t *testing.T) {
	sql := "\uFEFFINSERT INTO t (id) VALUES (1);"
	inserts, skips := collect(t, sql)
	if len(skips) != 0 || len(inserts) != 1 {
		t.Fatalf("inserts=%d skips=%+v", len(inserts), skips)
	}
}

func TestParseOnDuplicateDoesNotBreak(t *testing.T) {
	sql := `INSERT INTO t (id, n) VALUES (1, 'a') ON DUPLICATE KEY UPDATE n='b';`
	inserts, skips := collect(t, sql)
	if len(skips) != 0 || len(inserts) != 1 || inserts[0].rows[0][1].Text != "a" {
		t.Fatalf("inserts=%+v skips=%+v", inserts, skips)
	}
}

func TestParseLargeMultilineInsert(t *testing.T) {
	const n = 8000
	var b strings.Builder
	b.WriteString("INSERT INTO t (id, name) VALUES\n")
	for i := range n {
		if i > 0 {
			b.WriteString(",\n")
		}
		fmt.Fprintf(&b, "(%d, 'row-%d')", i, i)
	}
	b.WriteByte(';')

	count := 0
	var first, last []Cell
	err := Parse(strings.NewReader(b.String()), Handler{
		Begin: func(m Meta) error {
			if m.Table != "t" || len(m.Columns) != 2 {
				t.Fatalf("meta: %+v", m)
			}
			return nil
		},
		Row: func(cells []Cell) error {
			if count == 0 {
				first = append([]Cell(nil), cells...)
			}
			last = append([]Cell(nil), cells...)
			count++
			return nil
		},
		End: func() error { return nil },
		Skip: func(s Skip) {
			t.Fatalf("большой INSERT не должен пропускаться: %+v", s)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != n {
		t.Fatalf("строк: %d, ожидалось %d", count, n)
	}
	if first[0].Text != "0" || last[0].Text != "7999" || last[1].Text != "row-7999" {
		t.Fatalf("first=%+v last=%+v", first, last)
	}
}

func TestParseDoesNotReadAllIntoOneBuffer(t *testing.T) {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	began := make(chan struct{})
	go func() {
		defer close(done)
		err := Parse(pr, Handler{
			Begin: func(Meta) error {
				close(began)
				return nil
			},
			Row:  func([]Cell) error { return nil },
			End:  func() error { return nil },
			Skip: func(Skip) {},
		})
		done <- err
	}()
	if _, err := io.WriteString(pw, "INSERT INTO t (id) VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-began:
	case err := <-done:
		t.Fatalf("парсер завис или завершился до первой строки: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("парсер не отдал Begin, пока хвост INSERT ещё пишется в pipe — значит буферизует весь ввод")
	}
	if _, err := io.WriteString(pw, ",(2);"); err != nil {
		t.Fatal(err)
	}
	if err := pw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestParseTestdataFiles(t *testing.T) {
	root := "testdata"
	sql, err := os.ReadFile(filepath.Join(root, "several.sql"))
	if err != nil {
		t.Fatal(err)
	}
	inserts, skips := collect(t, string(sql))
	if len(skips) != 0 {
		t.Fatalf("skips: %+v", skips)
	}
	if len(inserts) != 2 {
		t.Fatalf("ожидалось 2 INSERT, получено %d", len(inserts))
	}
	if inserts[0].meta.Table != "alpha" || inserts[1].meta.Table != "beta" {
		t.Fatalf("таблицы: %q %q", inserts[0].meta.Table, inserts[1].meta.Table)
	}
	if len(inserts[1].rows) != 2 {
		t.Fatalf("у beta должно быть 2 строки, получено %d", len(inserts[1].rows))
	}
}

func FuzzParseNeverLeavesOpenInsert(f *testing.F) {
	for _, seed := range [][]byte{
		{},
		[]byte("not sql"),
		[]byte("INSERT INTO users (email) VALUES ('a@example.test');"),
		[]byte("INSERT INTO t (a,b) VALUES ('x' 'y'),;"),
		[]byte("\xEF\xBB\xBFINSERT INTO [用户] ([邮箱]) VALUES ('a');"),
		{0x00, 0xFF, 'I', 'N', 'S', 'E', 'R', 'T'},
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		active := false
		err := Parse(bytes.NewReader(data), Handler{
			Begin: func(Meta) error {
				if active {
					t.Fatal("повторный Begin без End/Skip")
				}
				active = true
				return nil
			},
			Row: func([]Cell) error {
				if !active {
					t.Fatal("Row без Begin")
				}
				return nil
			},
			End: func() error {
				if !active {
					t.Fatal("End без Begin")
				}
				active = false
				return nil
			},
			Skip: func(Skip) {
				active = false
			},
		})
		if err != nil {
			t.Fatalf("Parse с безошибочным reader: %v", err)
		}
		if active {
			t.Fatal("Parse завершился с незакрытым Begin")
		}
	})
}

func TestBeforeValuesSkipsCells(t *testing.T) {
	var rows int
	err := Parse(strings.NewReader("INSERT INTO t (email) VALUES ('a@example.test'), ('b');"), Handler{
		BeforeValues: func(Meta) (bool, error) { return true, nil },
		Begin: func(Meta) error {
			t.Fatal("Begin после отказа")
			return nil
		},
		Row: func([]Cell) error {
			rows++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("ячеек: %d", rows)
	}
}

// ValuesAt отдаёт смещения хвоста от начала потока (с BOM): вызывающий
// читает диапазон из того же файла и разбирает его ParseValues.
func TestValuesAtReportsStreamOffsets(t *testing.T) {
	sql := "\xEF\xBB\xBFINSERT INTO users (email) VALUES ('a@example.test');\n" +
		"INSERT INTO t SELECT 1;\n" +
		"INSERT INTO users (email) VALUES ('b@example.test'), ('c');"
	var tails []string
	err := Parse(strings.NewReader(sql), Handler{
		ValuesAt: func(m Meta, off, n int64) error {
			if m.Table != "users" {
				t.Fatalf("таблица %s", m.Table)
			}
			tails = append(tails, sql[off:off+n])
			return nil
		},
		Row: func([]Cell) error {
			t.Fatal("сканер не должен разбирать ячейки")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 2}
	if len(tails) != len(want) {
		t.Fatalf("хвосты: %q", tails)
	}
	for i, tail := range tails {
		rows := 0
		if err := ParseValues(strings.NewReader(tail), Meta{Table: "users", Columns: []string{"email"}}, Handler{
			Row: func([]Cell) error { rows++; return nil },
		}); err != nil {
			t.Fatal(err)
		}
		if rows != want[i] {
			t.Fatalf("хвост %q: строк %d, ожидалось %d", tail, rows, want[i])
		}
	}
}

// parsed — INSERT в виде, удобном для сравнения: «таблица(колонки)» и строки
// ячеек через «|». NULL/Missing — «∅».
type parsed struct {
	head string
	rows []string
}

func flatten(meta Meta, rows [][]Cell) parsed {
	p := parsed{head: meta.Table + "(" + strings.Join(meta.Columns, ",") + ")"}
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, c := range row {
			if c.Kind == Text {
				cells[i] = c.Text
			} else {
				cells[i] = "∅"
			}
		}
		p.rows = append(p.rows, strings.Join(cells, "|"))
	}
	return p
}

// collectSpill идёт продакшен-путём: сканер отдаёт границы хвоста statement
// (ValuesAt), ячейки из этого диапазона разбирает ParseValues.
func collectSpill(t *testing.T, sql string) []parsed {
	t.Helper()
	var out []parsed
	err := Parse(strings.NewReader(sql), Handler{
		ValuesAt: func(meta Meta, off, n int64) error {
			var rows [][]Cell
			ok := false
			err := ParseValues(io.NewSectionReader(strings.NewReader(sql), off, n), meta, Handler{
				Row: func(cells []Cell) error {
					rows = append(rows, append([]Cell(nil), cells...))
					return nil
				},
				End: func() error { ok = true; return nil },
			})
			if err != nil {
				return err
			}
			if ok {
				out = append(out, flatten(meta, rows))
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return out
}

// parseBoth проверяет, что последовательный разбор и путь через spill дают
// одно и то же, и возвращает результат.
func parseBoth(t *testing.T, sql string) []parsed {
	t.Helper()
	inserts, _ := collect(t, sql)
	var direct []parsed
	for _, in := range inserts {
		direct = append(direct, flatten(in.meta, in.rows))
	}
	spilled := collectSpill(t, sql)
	if fmt.Sprint(direct) != fmt.Sprint(spilled) {
		t.Fatalf("разбор расходится:\n Parse: %v\n spill: %v", direct, spilled)
	}
	return direct
}

func TestParseDialects(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want []parsed
	}{
		{
			name: "MSSQL: имя из трёх частей",
			sql:  "INSERT INTO [shop].[dbo].[users] ([email], [phone]) VALUES ('a@example.test', '555');",
			want: []parsed{{"users(email,phone)", []string{"a@example.test|555"}}},
		},
		{
			name: "MySQL/MSSQL: INSERT без INTO",
			sql: "INSERT users (email) VALUES ('a');\n" +
				"INSERT [dbo].[users] ([email]) VALUES ('b');",
			want: []parsed{
				{"users(email)", []string{"a"}},
				{"users(email)", []string{"b"}},
			},
		},
		{
			name: "MSSQL N'…' и Postgres E'…': префикс не попадает в ячейку",
			sql:  "INSERT INTO users (a, b, c) VALUES (N'Иван', n'O''Neil', E'x\\'y');",
			want: []parsed{{"users(a,b,c)", []string{"Иван|O'Neil|x'y"}}},
		},
		{
			name: "идентификатор на N без кавычки остаётся значением",
			sql:  "INSERT INTO users (a) VALUES (NOW());",
			want: []parsed{{"users(a)", []string{"NOW()"}}},
		},
		{
			name: "\\\" в двойных кавычках: сканер и парсер режут одинаково",
			sql: "INSERT INTO users (a, b) VALUES (\"x\\\"y\", 'z');\n" +
				"INSERT INTO users (a, b) VALUES ('q', 'w');",
			want: []parsed{
				{"users(a,b)", []string{"x\"y|z"}},
				{"users(a,b)", []string{"q|w"}},
			},
		},
		{
			name: "INSERT внутри $$-тела функции Postgres не данные",
			sql: "CREATE FUNCTION f() RETURNS trigger AS $$ BEGIN " +
				"INSERT INTO audit (email, phone) VALUES (NEW.email, NEW.phone); RETURN NEW; END; " +
				"$$ LANGUAGE plpgsql;\n" +
				"CREATE FUNCTION g() RETURNS void AS $body$ INSERT INTO audit (email) VALUES ('x'); $body$ LANGUAGE sql;\n" +
				"INSERT INTO users (email) VALUES ('a');",
			want: []parsed{{"users(email)", []string{"a"}}},
		},
		{
			name: "знак $ внутри имени не открывает $$-строку",
			sql:  "INSERT INTO a$b$c (email) VALUES ('a');\nINSERT INTO users (email) VALUES ('b');",
			want: []parsed{
				{"a$b$c(email)", []string{"a"}},
				{"users(email)", []string{"b"}},
			},
		},
		{
			name: "GRANT/TRIGGER с INSERT не дают таблицу",
			sql: "GRANT INSERT, UPDATE ON users TO app;\n" +
				"INSERT INTO users (email) VALUES ('a');",
			want: []parsed{{"users(email)", []string{"a"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBoth(t, tt.sql)
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Fatalf("\n got %v\nwant %v", got, tt.want)
			}
		})
	}
}

// Обрезанный дамп: INSERT оборван после целых строк (висящая запятая, конец
// файла, битая последняя строка). Целые строки сохраняются, обрыв — в Cut
// со строкой файла; следующий INSERT разбирается как обычно.
func TestParseCutInsertKeepsAcceptedRows(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		wantRows int
		wantLine int
		reason   string
	}{
		{"висящая запятая", "INSERT INTO t (email) VALUES\n('a'),\n('b'),;\nINSERT INTO ok (email) VALUES ('x');", 2, 3, "после запятой"},
		{"конец файла после запятой", "INSERT INTO t (email) VALUES\n('a'),\n('b'),\n", 2, 4, "после запятой"},
		{"битая последняя строка", "INSERT INTO t (email) VALUES\n('a'),\n('b'),\n('c", 2, 4, "битая строка"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, spill := range []bool{false, true} {
				var rows, cutLine int
				var reason string
				var tables []string
				h := Handler{
					Row:   func([]Cell) error { rows++; return nil },
					Cut:   func(r string, line int) { reason, cutLine = r, line },
					Begin: func(m Meta) error { tables = append(tables, m.Table); return nil },
					Skip:  func(sk Skip) { t.Fatalf("пропуск вместо обрыва: %+v", sk) },
				}
				if spill {
					h.ValuesAt = func(m Meta, off, n int64) error {
						return ParseValues(io.NewSectionReader(strings.NewReader(tt.sql), off, n), m, Handler{
							Row: h.Row, Cut: h.Cut, Skip: h.Skip,
							Begin: func(m Meta) error { tables = append(tables, m.Table); return nil },
						})
					}
				}
				if err := Parse(strings.NewReader(tt.sql), h); err != nil {
					t.Fatal(err)
				}
				if tables[0] != "t" || rows < tt.wantRows || !strings.Contains(reason, tt.reason) || cutLine != tt.wantLine {
					t.Fatalf("spill=%v: rows=%d reason=%q line=%d tables=%v", spill, rows, reason, cutLine, tables)
				}
			}
		})
	}
}
