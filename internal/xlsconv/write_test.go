package xlsconv

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sql2csv/internal/csvout"
)

func csvFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.EqualFold(filepath.Ext(e.Name()), ".csv") {
			names = append(names, e.Name())
		}
	}
	return names
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestWriteOneSheet(t *testing.T) {
	sheets := []specSheet{{
		Name: "Only",
		Rows: [][]string{
			{"id", "name"},
			{"1", "Ann"},
			{"2"},
		},
	}}
	for _, format := range []struct {
		name string
		fn   func(*testing.T, []specSheet) string
	}{
		{"xlsx", writeXLSX},
		{"xls", writeXLS},
	} {
		t.Run(format.name, func(t *testing.T) {
			src := format.fn(t, sheets)
			res := File(csvout.NewRegistry(), src)
			if res.OpenErr != nil {
				t.Fatal(res.OpenErr)
			}
			if res.SkipTooMany || res.CSV != 1 {
				t.Fatalf("skip=%v csv=%d", res.SkipTooMany, res.CSV)
			}
			wantName := "book_Only.csv"
			got := csvFiles(t, filepath.Dir(src))
			if len(got) != 1 || got[0] != wantName {
				t.Fatalf("файлы: %v, ожидался %s", got, wantName)
			}
			raw := readFile(t, filepath.Join(filepath.Dir(src), wantName))
			if bytes.HasPrefix([]byte(raw), []byte{0xEF, 0xBB, 0xBF}) {
				t.Fatal("BOM запрещён")
			}
			// первая строка листа — заголовок, дальше данные
			want := "\"id\",\"name\"\n\"1\",\"Ann\"\n\"2\",\"\"\n"
			if raw != want {
				t.Fatalf("CSV:\n got %q\nwant %q", raw, want)
			}
			if _, err := os.Stat(src); err != nil {
				t.Fatalf("исходный Excel изменён/удалён: %v", err)
			}
		})
	}
}

func TestWriteTwoSheetsTwoFiles(t *testing.T) {
	sheets := []specSheet{
		{Name: "First", Rows: [][]string{{"a"}}},
		{Name: "Second", Rows: [][]string{{"b"}}},
	}
	for _, format := range []struct {
		name string
		fn   func(*testing.T, []specSheet) string
	}{
		{"xlsx", writeXLSX},
		{"xls", writeXLS},
	} {
		t.Run(format.name, func(t *testing.T) {
			src := format.fn(t, sheets)
			res := File(csvout.NewRegistry(), src)
			if res.OpenErr != nil || res.CSV != 2 {
				t.Fatalf("err=%v csv=%d", res.OpenErr, res.CSV)
			}
			dir := filepath.Dir(src)
			got := csvFiles(t, dir)
			if len(got) != 2 {
				t.Fatalf("ожидалось 2 CSV, получено %v", got)
			}
			a := readFile(t, filepath.Join(dir, "book_First.csv"))
			b := readFile(t, filepath.Join(dir, "book_Second.csv"))
			if a != "\"a\"\n" || b != "\"b\"\n" {
				t.Fatalf("склеены или неверный формат:\nFirst %q\nSecond %q", a, b)
			}
		})
	}
}

func TestWriteSixSheetsNoCSV(t *testing.T) {
	sheets := make([]specSheet, 6)
	for i := range sheets {
		sheets[i] = specSheet{Name: string(rune('A' + i)), Rows: [][]string{{"x"}}}
	}
	for _, format := range []struct {
		name string
		fn   func(*testing.T, []specSheet) string
	}{
		{"xlsx", writeXLSX},
		{"xls", writeXLS},
	} {
		t.Run(format.name, func(t *testing.T) {
			src := format.fn(t, sheets)
			res := File(csvout.NewRegistry(), src)
			if res.OpenErr != nil {
				t.Fatal(res.OpenErr)
			}
			if !res.SkipTooMany || res.CSV != 0 {
				t.Fatalf("skip=%v csv=%d", res.SkipTooMany, res.CSV)
			}
			if got := csvFiles(t, filepath.Dir(src)); len(got) != 0 {
				t.Fatalf("при >5 листов CSV быть не должно: %v", got)
			}
		})
	}
}

func TestWriteExistingNameGetsIndex(t *testing.T) {
	sheets := []specSheet{{Name: "Only", Rows: [][]string{{"new"}}}}
	for _, format := range []struct {
		name string
		fn   func(*testing.T, []specSheet) string
	}{
		{"xlsx", writeXLSX},
		{"xls", writeXLS},
	} {
		t.Run(format.name, func(t *testing.T) {
			src := format.fn(t, sheets)
			dir := filepath.Dir(src)
			old := filepath.Join(dir, "book_Only.csv")
			if err := os.WriteFile(old, []byte("KEEP"), 0o644); err != nil {
				t.Fatal(err)
			}
			res := File(csvout.NewRegistry(), src)
			if res.OpenErr != nil || res.CSV != 1 {
				t.Fatalf("err=%v csv=%d", res.OpenErr, res.CSV)
			}
			if filepath.Base(res.Paths[0]) != "book_Only(1).csv" {
				t.Fatalf("ожидался book_Only(1).csv, получено %s", res.Paths[0])
			}
			if readFile(t, old) != "KEEP" {
				t.Fatal("чужой CSV затёрт")
			}
			if readFile(t, res.Paths[0]) != "\"new\"\n" {
				t.Fatalf("новый CSV: %q", readFile(t, res.Paths[0]))
			}
		})
	}
}

func TestWriteQuoteEscaped(t *testing.T) {
	const raw = `he said "hello"`
	sheets := []specSheet{{Name: "Q", Rows: [][]string{{raw}}}}
	for _, format := range []struct {
		name string
		fn   func(*testing.T, []specSheet) string
	}{
		{"xlsx", writeXLSX},
		{"xls", writeXLS},
	} {
		t.Run(format.name, func(t *testing.T) {
			src := format.fn(t, sheets)
			res := File(csvout.NewRegistry(), src)
			if res.OpenErr != nil || res.CSV != 1 {
				t.Fatalf("err=%v csv=%d", res.OpenErr, res.CSV)
			}
			got := readFile(t, res.Paths[0])
			want := "\"he said \"\"hello\"\"\"\n"
			if got != want {
				t.Fatalf("экранирование кавычки:\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestWriteEmptySheetSkipped(t *testing.T) {
	sheets := []specSheet{
		{Name: "Blank", Rows: nil},
		{Name: "Data", Rows: [][]string{{"ok"}}},
	}
	for _, format := range []struct {
		name string
		fn   func(*testing.T, []specSheet) string
	}{
		{"xlsx", writeXLSX},
		{"xls", writeXLS},
	} {
		t.Run(format.name, func(t *testing.T) {
			src := format.fn(t, sheets)
			res := File(csvout.NewRegistry(), src)
			if res.OpenErr != nil || res.CSV != 1 {
				t.Fatalf("err=%v csv=%d", res.OpenErr, res.CSV)
			}
			dir := filepath.Dir(src)
			got := csvFiles(t, dir)
			if len(got) != 1 || got[0] != "book_Data.csv" {
				t.Fatalf("файлы: %v", got)
			}
			if _, err := os.Stat(filepath.Join(dir, "book_Blank.csv")); err == nil {
				t.Fatal("пустой лист не должен создавать CSV")
			}
		})
	}
}

func TestWriteDoesNotAppendToSQLCSV(t *testing.T) {
	src := writeXLSX(t, []specSheet{{Name: "Only", Rows: [][]string{{"excel"}}}})
	dir := filepath.Dir(src)
	reg := csvout.NewRegistry()
	w, err := csvout.Create(reg, dir, "book_Only", []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Row([]string{"sql"}); err != nil {
		t.Fatal(err)
	}
	sqlRes, err := w.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(sqlRes.Path) != "book_Only.csv" {
		t.Fatalf("SQL CSV: %s", sqlRes.Path)
	}

	res := File(reg, src)
	if res.OpenErr != nil || res.CSV != 1 {
		t.Fatalf("err=%v csv=%d", res.OpenErr, res.CSV)
	}
	if filepath.Base(res.Paths[0]) != "book_Only(1).csv" {
		t.Fatalf("Excel не должен дописывать SQL-файл: %s", res.Paths[0])
	}
	sqlRaw := readFile(t, sqlRes.Path)
	if !strings.Contains(sqlRaw, "\"id\"") || strings.Contains(sqlRaw, "excel") {
		t.Fatalf("SQL CSV испорчен: %q", sqlRaw)
	}
	if readFile(t, res.Paths[0]) != "\"excel\"\n" {
		t.Fatalf("Excel CSV: %q", readFile(t, res.Paths[0]))
	}
}

func TestCsvBaseEmptyParts(t *testing.T) {
	if got := csvBase("", ""); got != "book_sheet" {
		t.Fatalf("csvBase=%q", got)
	}
	if got := csvBase("CON", "AUX"); got != "_CON__AUX" {
		t.Fatalf("зарезервированные части: %q", got)
	}
}
