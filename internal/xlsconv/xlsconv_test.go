package xlsconv

import (
	"os"
	"path/filepath"
	"testing"
)

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
