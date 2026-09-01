package xlsconv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadOneSheetXLSX(t *testing.T) {
	path := writeXLSX(t, []specSheet{{
		Name: "Only",
		Rows: [][]string{
			{"id", "name"},
			{"1", "Ann"},
		},
	}})
	book, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	assertSheetNames(t, book, "Only")
	sh := book.Sheets[0]
	if sh.Empty {
		t.Fatal("лист с данными не должен быть пустым")
	}
	if cell(t, sh, 0, 0) != "id" || cell(t, sh, 1, 1) != "Ann" {
		t.Fatalf("неожиданные ячейки: %#v", sh.Rows)
	}
}

func TestReadTwoSheetsXLSX(t *testing.T) {
	path := writeXLSX(t, []specSheet{
		{Name: "First", Rows: [][]string{{"a"}}},
		{Name: "Second", Rows: [][]string{{"b"}}},
	})
	book, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	assertSheetNames(t, book, "First", "Second")
	if cell(t, book.Sheets[0], 0, 0) != "a" || cell(t, book.Sheets[1], 0, 0) != "b" {
		t.Fatalf("значения листов: %#v %#v", book.Sheets[0].Rows, book.Sheets[1].Rows)
	}
}

func TestReadSixSheetsSkipXLSX(t *testing.T) {
	sheets := make([]specSheet, 6)
	for i := range sheets {
		sheets[i] = specSheet{Name: string(rune('A' + i)), Rows: [][]string{{"x"}}}
	}
	path := writeXLSX(t, sheets)
	book, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !book.SkipTooMany {
		t.Fatal("ожидали пропуск книги с 6 листами")
	}
	if len(book.Sheets) != 0 {
		t.Fatalf("при skip листы отдавать нельзя: %#v", book.Sheets)
	}
}

func TestReadFiveSheetsStillInScopeXLSX(t *testing.T) {
	sheets := make([]specSheet, 5)
	for i := range sheets {
		sheets[i] = specSheet{Name: string(rune('A' + i)), Rows: [][]string{{"x"}}}
	}
	book, err := Read(writeXLSX(t, sheets))
	if err != nil {
		t.Fatal(err)
	}
	if book.SkipTooMany || len(book.Sheets) != 5 {
		t.Fatalf("5 листов в scope: skip=%v n=%d", book.SkipTooMany, len(book.Sheets))
	}
}

func TestReadEmptySheetXLSX(t *testing.T) {
	path := writeXLSX(t, []specSheet{
		{Name: "Blank", Rows: nil},
		{Name: "Data", Rows: [][]string{{"ok"}}},
	})
	book, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	assertSheetNames(t, book, "Blank", "Data")
	if !book.Sheets[0].Empty {
		t.Fatalf("пустой лист не помечен: %#v", book.Sheets[0])
	}
	if len(book.Sheets[0].Rows) != 0 {
		t.Fatalf("у пустого листа не должно быть строк: %#v", book.Sheets[0].Rows)
	}
	if book.Sheets[1].Empty || cell(t, book.Sheets[1], 0, 0) != "ok" {
		t.Fatalf("непустой сосед: %#v", book.Sheets[1])
	}
}

func TestReadQuoteInCellXLSX(t *testing.T) {
	const raw = `he said "hello"`
	path := writeXLSX(t, []specSheet{{
		Name: "Q",
		Rows: [][]string{{raw}},
	}})
	book, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	got := cell(t, book.Sheets[0], 0, 0)
	if got != raw {
		t.Fatalf("кавычка должна остаться данными, не CSV: %q", got)
	}
}

func TestReadTrailingEmptyRowsDroppedXLSX(t *testing.T) {
	path := writeXLSX(t, []specSheet{{
		Name: "T",
		Rows: [][]string{
			{"keep"},
			{""},
			{""},
		},
	}})
	book, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	sh := book.Sheets[0]
	if len(sh.Rows) != 1 || cell(t, sh, 0, 0) != "keep" {
		t.Fatalf("хвостовые пустые строки должны отброситься: %#v", sh.Rows)
	}
}

func TestReadHiddenSheetsXLSX(t *testing.T) {
	path := writeXLSX(t, []specSheet{
		{Name: "Visible", Rows: [][]string{{"v"}}},
		{Name: "Hidden", Rows: [][]string{{"h"}}, Hidden: true},
		{Name: "Very", Rows: [][]string{{"w"}}, VeryHidden: true},
	})
	book, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	assertSheetNames(t, book, "Visible", "Hidden", "Very")
	if cell(t, book.Sheets[1], 0, 0) != "h" || cell(t, book.Sheets[2], 0, 0) != "w" {
		t.Fatalf("скрытые листы должны читаться: %#v", book.Sheets)
	}
}

func TestReadUnsupportedExt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.xlsm")
	if err := os.WriteFile(path, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("ожидалась ошибка для .xlsm")
	}
}

func TestReadOneSheetXLS(t *testing.T) {
	path := writeXLS(t, []specSheet{{
		Name: "Only",
		Rows: [][]string{
			{"id", "name"},
			{"1", "Ann"},
		},
	}})
	book, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	assertSheetNames(t, book, "Only")
	if book.Sheets[0].Empty || cell(t, book.Sheets[0], 1, 1) != "Ann" {
		t.Fatalf("one.xls: %#v", book.Sheets[0].Rows)
	}
}

func TestReadTwoSheetsXLS(t *testing.T) {
	path := writeXLS(t, []specSheet{
		{Name: "First", Rows: [][]string{{"a"}}},
		{Name: "Second", Rows: [][]string{{"b"}}},
	})
	book, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	assertSheetNames(t, book, "First", "Second")
	if cell(t, book.Sheets[0], 0, 0) != "a" || cell(t, book.Sheets[1], 0, 0) != "b" {
		t.Fatalf("two.xls: %#v %#v", book.Sheets[0].Rows, book.Sheets[1].Rows)
	}
}

func TestReadSixSheetsSkipXLS(t *testing.T) {
	sheets := make([]specSheet, 6)
	for i := range sheets {
		sheets[i] = specSheet{Name: string(rune('A' + i)), Rows: [][]string{{"x"}}}
	}
	book, err := Read(writeXLS(t, sheets))
	if err != nil {
		t.Fatal(err)
	}
	if !book.SkipTooMany {
		t.Fatal("ожидали пропуск six.xls")
	}
	if len(book.Sheets) != 0 {
		t.Fatalf("при skip листы отдавать нельзя: %#v", book.Sheets)
	}
}

func TestReadEmptySheetXLS(t *testing.T) {
	path := writeXLS(t, []specSheet{
		{Name: "Blank", Rows: nil},
		{Name: "Data", Rows: [][]string{{"ok"}}},
	})
	book, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	assertSheetNames(t, book, "Blank", "Data")
	if !book.Sheets[0].Empty || len(book.Sheets[0].Rows) != 0 {
		t.Fatalf("пустой лист: %#v", book.Sheets[0])
	}
	if book.Sheets[1].Empty || cell(t, book.Sheets[1], 0, 0) != "ok" {
		t.Fatalf("непустой сосед: %#v", book.Sheets[1])
	}
}

func TestReadQuoteInCellXLS(t *testing.T) {
	const raw = `he said "hello"`
	path := writeXLS(t, []specSheet{{
		Name: "Q",
		Rows: [][]string{{raw}},
	}})
	book, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	got := cell(t, book.Sheets[0], 0, 0)
	if got != raw {
		t.Fatalf("кавычка должна остаться данными, не CSV: %q", got)
	}
}
