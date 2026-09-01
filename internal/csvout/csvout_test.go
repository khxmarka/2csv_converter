package csvout

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"sql2csv/internal/insert"
)

func TestEncodeRowSnapshot(t *testing.T) {
	got := encodeRow([]string{"id", "name", `say "hi"`})
	want := "\"id\",\"name\",\"say \"\"hi\"\"\"\n"
	if got != want {
		t.Fatalf("encodeRow:\n got %q\nwant %q", got, want)
	}
	if len(got) >= 3 && got[0] == 0xEF && got[1] == 0xBB && got[2] == 0xBF {
		t.Fatal("BOM не должен появляться")
	}
}

func TestFileBase(t *testing.T) {
	cases := map[string]string{
		"users":    "users",
		"foo<bar>": "foo_bar_",
		"CON":      "_CON",
		"con":      "_con",
		"COM1":     "_COM1",
		"aux.data": "_aux.data",
		"end.":     "end",
		"end ":     "end",
		`a/b\c`:    "a_b_c",
		"":         "table",
		"***":      "___",
		"??":       "__",
	}
	for in, want := range cases {
		if got := FileBase(in); got != want {
			t.Fatalf("FileBase(%q)=%q, ожидалось %q", in, got, want)
		}
	}
	if got := FileBaseDefault("", "book"); got != "book" {
		t.Fatalf("FileBaseDefault empty book: %q", got)
	}
	if got := FileBaseDefault("", "sheet"); got != "sheet" {
		t.Fatalf("FileBaseDefault empty sheet: %q", got)
	}
}

func TestNormalizeRow(t *testing.T) {
	cells := []insert.Cell{
		{Kind: insert.Text, Text: "1"},
		{Kind: insert.Null},
		{Kind: insert.Missing},
	}
	row, padded, err := NormalizeRow(cells, 5)
	if err != nil || !padded {
		t.Fatalf("padded=%v err=%v", padded, err)
	}
	want := []string{"1", "", "", "", ""}
	if len(row) != 5 || row[0] != "1" || row[1] != "" || row[4] != "" {
		t.Fatalf("row=%q want=%q", row, want)
	}
	_, _, err = NormalizeRow(cells, 2)
	if err != ErrTooManyValues {
		t.Fatalf("лишние значения: %v", err)
	}
	row, padded, err = NormalizeRow(cells, 0)
	if err != nil || padded {
		t.Fatalf("nCol=0: padded=%v err=%v", padded, err)
	}
	if len(row) != 3 || row[0] != "1" || row[1] != "" || row[2] != "" {
		t.Fatalf("nCol=0 row=%q", row)
	}
}

func TestCommitBytesAndNoBOM(t *testing.T) {
	dir := t.TempDir()
	w, err := Create(NewRegistry(), dir, "users", []string{"id", "name"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Row([]string{"1", `say "hi"`}); err != nil {
		t.Fatal(err)
	}
	if err := w.Row([]string{"", ""}); err != nil {
		t.Fatal(err)
	}
	res, err := w.Commit()
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(dir, "users.csv")
	if res.Path != wantPath {
		t.Fatalf("path=%q want=%q", res.Path, wantPath)
	}
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("BOM запрещён")
	}
	want := "\"id\",\"name\"\n\"1\",\"say \"\"hi\"\"\"\n\"\",\"\"\n"
	if string(raw) != want {
		t.Fatalf("CSV:\n got %q\nwant %q", raw, want)
	}
	if bytes.Contains(raw, []byte("INSERT")) {
		t.Fatal("в CSV не должно быть SQL")
	}
}

func TestExistingFileGetsIndex(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "t.csv")
	if err := os.WriteFile(old, []byte("KEEP"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := Create(NewRegistry(), dir, "t", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Row([]string{"1"}); err != nil {
		t.Fatal(err)
	}
	res, err := w.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(res.Path) != "t(1).csv" {
		t.Fatalf("ожидался t(1).csv, получено %s", res.Path)
	}
	keep, _ := os.ReadFile(old)
	if string(keep) != "KEEP" {
		t.Fatalf("существующий файл затёрт: %q", keep)
	}
}

func TestAbortLeavesNoCSV(t *testing.T) {
	dir := t.TempDir()
	w, err := Create(NewRegistry(), dir, "t", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Row([]string{"1"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Fatalf("после Abort директория должна быть пуста, осталось %s", e.Name())
	}
}

func TestShortRowPadded(t *testing.T) {
	dir := t.TempDir()
	w, err := Create(NewRegistry(), dir, "t", []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Row([]string{"1"}); err != nil {
		t.Fatal(err)
	}
	res, err := w.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if res.PaddedRows != 1 {
		t.Fatalf("padded=%d", res.PaddedRows)
	}
	raw, _ := os.ReadFile(res.Path)
	want := "\"a\",\"b\",\"c\"\n\"1\",\"\",\"\"\n"
	if string(raw) != want {
		t.Fatalf("got %q want %q", raw, want)
	}
}

func commitInsert(t *testing.T, reg *Registry, dir, table string, cols []string, rows ...[]string) Result {
	t.Helper()
	w, err := Create(reg, dir, table, cols)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if err := w.Row(row); err != nil {
			_ = w.Abort()
			t.Fatal(err)
		}
	}
	res, err := w.Commit()
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func readCSV(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestMergeTwoInsertsSameKeyOneFile(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	a := commitInsert(t, reg, dir, "t", []string{"id", "item"}, []string{"1", "first"})
	b := commitInsert(t, reg, dir, "t", []string{"x", "y"}, []string{"2", "second"})
	if filepath.Base(a.Path) != "t.csv" || a.Path != b.Path {
		t.Fatalf("пути: %s и %s", a.Path, b.Path)
	}
	if !b.Appended {
		t.Fatal("второй INSERT должен дописывать")
	}
	if _, err := os.Stat(filepath.Join(dir, "t(1).csv")); err == nil {
		t.Fatal("не должно быть t(1).csv")
	}
	got := readCSV(t, a.Path)
	want := "\"id\",\"item\"\n\"1\",\"first\"\n\"2\",\"second\"\n"
	if got != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
	}
}

func TestMergeTwoDirsStaySeparate(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "Alpha")
	beta := filepath.Join(root, "Beta")
	reg := NewRegistry()
	commitInsert(t, reg, alpha, "t", []string{"id"}, []string{"1"})
	commitInsert(t, reg, beta, "t", []string{"id"}, []string{"2"})
	if readCSV(t, filepath.Join(alpha, "t.csv")) != "\"id\"\n\"1\"\n" {
		t.Fatal("Alpha/t.csv")
	}
	if readCSV(t, filepath.Join(beta, "t.csv")) != "\"id\"\n\"2\"\n" {
		t.Fatal("Beta/t.csv")
	}
}

func TestMergePreexistingTakesIndexThenAppends(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "t.csv")
	if err := os.WriteFile(old, []byte("KEEP"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	a := commitInsert(t, reg, dir, "t", []string{"id"}, []string{"1"})
	b := commitInsert(t, reg, dir, "t", []string{"id"}, []string{"2"})
	if filepath.Base(a.Path) != "t(1).csv" || a.Path != b.Path {
		t.Fatalf("ожидалась склейка в t(1).csv, получено %s и %s", a.Path, b.Path)
	}
	if _, err := os.Stat(filepath.Join(dir, "t(2).csv")); err == nil {
		t.Fatal("не должно быть t(2).csv")
	}
	keep, _ := os.ReadFile(old)
	if string(keep) != "KEEP" {
		t.Fatalf("чужой t.csv затёрт: %q", keep)
	}
	got := readCSV(t, a.Path)
	want := "\"id\"\n\"1\"\n\"2\"\n"
	if got != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
	}
}

func TestMergeSecondInsertTooManySkipped(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	first := commitInsert(t, reg, dir, "t", []string{"id", "item"}, []string{"1", "ok"})
	w, err := Create(reg, dir, "t", []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if w.NCol() != 2 {
		t.Fatalf("ширина заголовка=%d", w.NCol())
	}
	if err := w.Row([]string{"2", "x", "y"}); err != ErrTooManyValues {
		_ = w.Abort()
		t.Fatalf("ожидался ErrTooManyValues, получено %v", err)
	}
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}
	got := readCSV(t, first.Path)
	want := "\"id\",\"item\"\n\"1\",\"ok\"\n"
	if got != want {
		t.Fatalf("первый CSV испорчен:\n got %q\nwant %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "t(1).csv")); err == nil {
		t.Fatal("провал второго INSERT не должен плодить файл")
	}
}

func TestMergeNoSecondHeaderLine(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	commitInsert(t, reg, dir, "t", []string{"id", "item"}, []string{"1", "a"}, []string{"2", "b"})
	commitInsert(t, reg, dir, "t", []string{"id", "item"}, []string{"3", "c"})
	got := readCSV(t, filepath.Join(dir, "t.csv"))
	header := "\"id\",\"item\"\n"
	if bytes.Count([]byte(got), []byte(header)) != 1 {
		t.Fatalf("заголовок должен быть один раз:\n%s", got)
	}
	want := "\"id\",\"item\"\n\"1\",\"a\"\n\"2\",\"b\"\n\"3\",\"c\"\n"
	if got != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
	}
}

func TestConcurrentSameTableSerialized(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	const n = 8
	var wg sync.WaitGroup
	errc := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			w, err := Create(reg, dir, "same", []string{"id"})
			if err != nil {
				errc <- err
				return
			}
			if err := w.Row([]string{"1"}); err != nil {
				_ = w.Abort()
				errc <- err
				return
			}
			_, err = w.Commit()
			errc <- err
		}()
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		if err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "same.csv" {
		t.Fatalf("файлов: %v, ожидался один same.csv", entries)
	}
	got := readCSV(t, filepath.Join(dir, "same.csv"))
	if bytes.Count([]byte(got), []byte("\"id\"\n")) != 1 {
		t.Fatalf("заголовок не один:\n%s", got)
	}
	if bytes.Count([]byte(got), []byte("\"1\"\n")) != n {
		t.Fatalf("строк данных: %q", got)
	}
}

func TestReservedTableNameFile(t *testing.T) {
	dir := t.TempDir()
	w, err := Create(NewRegistry(), dir, "NUL", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Row([]string{"1"}); err != nil {
		t.Fatal(err)
	}
	res, err := w.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(res.Path) != "_NUL.csv" {
		t.Fatalf("зарезервированное имя: %s", res.Path)
	}
}

func TestCreateNoHeaderFromEmptyColumns(t *testing.T) {
	dir := t.TempDir()
	w, err := Create(NewRegistry(), dir, "dle_xfsearch", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Row([]string{"334", "10", "genre", "Action"}); err != nil {
		t.Fatal(err)
	}
	if w.NCol() != 4 {
		t.Fatalf("ширина после первой строки: %d", w.NCol())
	}
	if err := w.Row([]string{"335", "10", "genre", "Adventure"}); err != nil {
		t.Fatal(err)
	}
	res, err := w.Commit()
	if err != nil {
		t.Fatal(err)
	}
	got := readCSV(t, res.Path)
	want := "\"334\",\"10\",\"genre\",\"Action\"\n\"335\",\"10\",\"genre\",\"Adventure\"\n"
	if got != want {
		t.Fatalf("CSV без шапки:\n got %q\nwant %q", got, want)
	}
}

func TestCreatePlainHasHeaderAndNoMerge(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	sql := commitInsert(t, reg, dir, "t", []string{"id"}, []string{"1"})
	w, err := CreatePlain(reg, dir, "t", []string{"col"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Row([]string{"excel"}); err != nil {
		t.Fatal(err)
	}
	plain, err := w.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(plain.Path) != "t(1).csv" {
		t.Fatalf("Excel не должен дописывать INSERT: %s", plain.Path)
	}
	if readCSV(t, sql.Path) != "\"id\"\n\"1\"\n" {
		t.Fatalf("SQL CSV испорчен: %q", readCSV(t, sql.Path))
	}
	got := readCSV(t, plain.Path)
	if got != "\"col\"\n\"excel\"\n" {
		t.Fatalf("заголовок + данные: %q", got)
	}
}

func TestCreatePlainAbortKeepsFirst(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	w1, err := CreatePlain(reg, dir, "book_First", []string{"h"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w1.Row([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	first, err := w1.Commit()
	if err != nil {
		t.Fatal(err)
	}
	w2, err := CreatePlain(reg, dir, "book_Second", []string{"h"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w2.Row([]string{"b"}); err != nil {
		t.Fatal(err)
	}
	if err := w2.Abort(); err != nil {
		t.Fatal(err)
	}
	if readCSV(t, first.Path) != "\"h\"\n\"a\"\n" {
		t.Fatal("провал второго листа удалил первый CSV")
	}
	if _, err := os.Stat(filepath.Join(dir, "book_Second.csv")); err == nil {
		t.Fatal("после Abort второго листа CSV быть не должно")
	}
}
