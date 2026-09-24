package xlsconv

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"sql2csv/internal/csvout"

	"github.com/xuri/excelize/v2"
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

func TestStreamingXLSXPreservesInnerEmptyRowsAndDropsTrailing(t *testing.T) {
	src := writeXLSX(t, []specSheet{{
		Name: "Rows",
		Rows: [][]string{
			{"header"},
			{""},
			{"value"},
			{""},
			{""},
		},
	}})
	res := File(csvout.NewRegistry(), src)
	if res.OpenErr != nil || res.WriteErr != nil || res.CSV != 1 {
		t.Fatalf("результат: %+v", res)
	}
	got := readFile(t, res.Paths[0])
	want := "\"header\"\n\"\"\n\"value\"\n"
	if got != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
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

// §13: ширина = максимум колонок среди строк; короткая шапка и строки
// дополняются "", внутренняя пустая строка сохраняется, хвостовая — нет.
func TestWriteXLSXWidthFromWidestRow(t *testing.T) {
	src := writeXLSX(t, []specSheet{{
		Name: "W",
		Rows: [][]string{
			{"h"},
			{"a", "b", "c"},
			{""},
			{"d"},
			{""},
		},
	}})
	res := File(csvout.NewRegistry(), src)
	if res.WriteErr != nil || res.CSV != 1 {
		t.Fatalf("результат: %+v", res)
	}
	want := "\"h\",\"\",\"\"\n\"a\",\"b\",\"c\"\n\"\",\"\",\"\"\n\"d\",\"\",\"\"\n"
	if got := readFile(t, res.Paths[0]); got != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
	}
}

// §13: скрытые и очень скрытые листы тоже дают CSV; 5 листов — ещё в scope.
func TestWriteHiddenAndFiveSheetsXLSX(t *testing.T) {
	src := writeXLSX(t, []specSheet{
		{Name: "Visible", Rows: [][]string{{"v"}}},
		{Name: "Hidden", Rows: [][]string{{"h"}}, Hidden: true},
		{Name: "Very", Rows: [][]string{{"w"}}, VeryHidden: true},
		{Name: "Four", Rows: [][]string{{"4"}}},
		{Name: "Five", Rows: [][]string{{"5"}}},
	})
	res := File(csvout.NewRegistry(), src)
	if res.OpenErr != nil || res.WriteErr != nil || res.SkipTooMany || res.CSV != 5 {
		t.Fatalf("результат: %+v", res)
	}
	dir := filepath.Dir(src)
	if got := readFile(t, filepath.Join(dir, "book_Hidden.csv")); got != "\"h\"\n" {
		t.Fatalf("скрытый лист: %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "book_Very.csv")); got != "\"w\"\n" {
		t.Fatalf("очень скрытый лист: %q", got)
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

func TestChartSheetCountsTowardWorkbookLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "charts.xlsx")
	book := excelize.NewFile()
	for i := 2; i <= 5; i++ {
		if _, err := book.NewSheet("Sheet" + strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := book.SetCellValue("Sheet1", "A1", "category"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellValue("Sheet1", "A2", "A"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellValue("Sheet1", "B1", "value"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellValue("Sheet1", "B2", 1); err != nil {
		t.Fatal(err)
	}
	if err := book.AddChartSheet("Chart1", &excelize.Chart{
		Type: excelize.Col,
		Series: []excelize.ChartSeries{{
			Name:       "Sheet1!$B$1",
			Categories: "Sheet1!$A$2",
			Values:     "Sheet1!$B$2",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := book.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}

	res := File(csvout.NewRegistry(), path)
	if !res.SkipTooMany || res.CSV != 0 {
		t.Fatalf("chart sheet должен быть шестым листом: %+v", res)
	}
	if got := csvFiles(t, dir); len(got) != 0 {
		t.Fatalf("при шести листах CSV быть не должно: %v", got)
	}
}

func TestWriteExistingNameIsOverwritten(t *testing.T) {
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
			if filepath.Base(res.Paths[0]) != "book_Only.csv" {
				t.Fatalf("ожидался book_Only.csv, получено %s", res.Paths[0])
			}
			if readFile(t, old) != "\"new\"\n" {
				t.Fatalf("CSV не перезаписан: %q", readFile(t, old))
			}
			if _, err := os.Stat(filepath.Join(dir, "book_Only(1).csv")); !os.IsNotExist(err) {
				t.Fatalf("book_Only(1).csv не должен создаваться, err=%v", err)
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

func TestWriteDoesNotOverwriteSQLCSVOfThisRun(t *testing.T) {
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
	if res.OpenErr != nil || res.CSV != 0 || !errors.Is(res.WriteErr, csvout.ErrNameTaken) {
		t.Fatalf("err=%v csv=%d writeErr=%v", res.OpenErr, res.CSV, res.WriteErr)
	}
	if sqlRaw := readFile(t, sqlRes.Path); sqlRaw != "\"id\"\n\"sql\"\n" {
		t.Fatalf("CSV SQL затёрт или дописан: %q", sqlRaw)
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
