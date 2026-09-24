package csvout

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitExactThresholdUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rows.csv")
	writePlainRows(t, path, "\"id\"\n", "\"a\"\n", SplitThreshold)
	before := fileSHA(t, path)

	res, err := SplitIfNeeded(path, SplitWithHeader)
	if err != nil {
		t.Fatal(err)
	}
	if res.Split || res.DataRows != SplitThreshold {
		t.Fatalf("split=%v rows=%d", res.Split, res.DataRows)
	}
	if fileSHA(t, path) != before {
		t.Fatal("файл на пороге изменился")
	}
	if _, err := os.Stat(filepath.Join(dir, "rows_2.csv")); !os.IsNotExist(err) {
		t.Fatalf("rows_2.csv не должен появляться: %v", err)
	}
	assertNoTemps(t, dir)
}

func TestSplitJustOverThreshold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rows.csv")
	const rows = SplitThreshold + 1
	writePlainRows(t, path, "\"id\"\n", "\"a\"\n", rows)

	res, err := SplitIfNeeded(path, SplitWithHeader)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Split || res.DataRows != rows {
		t.Fatalf("split=%v rows=%d", res.Split, res.DataRows)
	}
	assertPartData(t, res.Parts, SplitWithHeader, []int64{SplitChunkRows, SplitChunkRows, 1})
	assertNoTemps(t, dir)
}

func TestSplit1200001Distribution(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rows.csv")
	const rows = 1_200_001
	writePlainRows(t, path, "\"id\"\n", "\"a\"\n", rows)

	res, err := SplitIfNeeded(path, SplitWithHeader)
	if err != nil {
		t.Fatal(err)
	}
	assertPartData(t, res.Parts, SplitWithHeader, []int64{500_000, 500_000, 200_001})
	for _, p := range res.Parts {
		raw := readHead(t, p, len("\"id\"\n"))
		if string(raw) != "\"id\"\n" {
			t.Fatalf("%s: заголовок %q", p, raw)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "rows_4.csv")); !os.IsNotExist(err) {
		t.Fatalf("лишняя часть: %v", err)
	}
	assertNoTemps(t, dir)
}

func TestSplitHeaderRepeated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.csv")
	writeRaw(t, path, "\"h\"\n\"a\"\n\"b\"\n\"c\"\n")

	res, err := splitFile(path, SplitWithHeader, 2, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertPartBytes(t, res.Parts, []string{
		"\"h\"\n\"a\"\n\"b\"\n",
		"\"h\"\n\"c\"\n",
	})
}

func TestSplitNoHeaderDoesNotInventHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.csv")
	writeRaw(t, path, "\"a\"\n\"b\"\n\"c\"\n")

	res, err := splitFile(path, SplitNoHeader, 2, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertPartBytes(t, res.Parts, []string{
		"\"a\"\n\"b\"\n",
		"\"c\"\n",
	})
}

func TestSplitForeignFirstNonEmptyIsHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.csv")
	writeRaw(t, path, "\n\n\"h\"\n\"a\"\n\"b\"\n")

	res, err := splitFile(path, SplitWithHeader, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertPartBytes(t, res.Parts, []string{
		"\"h\"\n\"a\"\n",
		"\"h\"\n\"b\"\n",
	})

	kept := filepath.Join(dir, "small.csv")
	const raw = "\n\"h\"\r\n\"a\"\r\n\r\n"
	writeRaw(t, kept, raw)
	before := fileSHA(t, kept)
	out, err := SplitIfNeeded(kept, SplitWithHeader)
	if err != nil {
		t.Fatal(err)
	}
	if out.Split || out.DataRows != 1 || fileSHA(t, kept) != before {
		t.Fatalf("малый чужой CSV изменился: %+v", out)
	}
}

// §14: уже лежащий {stem}_2.csv становится слотом части и заменяется.
// Поэтому повторная нарезка после обрыва не плодит дубли в _3, _4.
func TestSplitReplacesOccupiedPartName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.csv")
	writeRaw(t, path, "\"a\"\n\"b\"\n\"c\"\n")
	writeRaw(t, filepath.Join(dir, "users_2.csv"), "STALE\n")

	res, err := splitFile(path, SplitNoHeader, 2, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertPartBytes(t, res.Parts, []string{
		"\"a\"\n\"b\"\n",
		"\"c\"\n",
	})
	if filepath.Base(res.Parts[1]) != "users_2.csv" {
		t.Fatalf("часть: %s", res.Parts[1])
	}
	if _, err := os.Stat(filepath.Join(dir, "users_3.csv")); !os.IsNotExist(err) {
		t.Fatalf("лишняя часть users_3.csv: %v", err)
	}
}

// §14: имя вида {stem}_N режется от своего stem, число в хвосте не продолжается.
func TestSplitPartNamesUseOwnStem(t *testing.T) {
	tests := []struct {
		name string
		file string
		want []string
	}{
		{"часть прошлой нарезки", "users_2.csv", []string{"users_2.csv", "users_2_2.csv"}},
		{"год в имени", "orders_2024.csv", []string{"orders_2024.csv", "orders_2024_2.csv"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tt.file)
			writeRaw(t, path, "\"a\"\n\"b\"\n\"c\"\n")

			res, err := splitFile(path, SplitNoHeader, 2, 2, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Parts) != len(tt.want) {
				t.Fatalf("части: %v", res.Parts)
			}
			for i, p := range res.Parts {
				if filepath.Base(p) != tt.want[i] {
					t.Fatalf("части: %v, ожидалось %v", res.Parts, tt.want)
				}
			}
		})
	}
}

// §14: провал нарезки оставляет монолит как был; части, лежавшие до прогона,
// не уничтожаются, новые части этого прогона убираются.
func TestSplitFailureKeepsMonolithAndPreexistingParts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.csv")
	const body = "\"a\"\n\"b\"\n\"c\"\n\"d\"\n\"e\"\n\"f\"\n\"g\"\n"
	writeRaw(t, path, body)
	writeRaw(t, filepath.Join(dir, "users_3.csv"), "OLD\n")
	// Каталог на месте четвёртой части: её публикация падает.
	if err := os.Mkdir(filepath.Join(dir, "users_4.csv"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "users_4.csv", "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := splitFile(path, SplitNoHeader, 2, 2, nil); err == nil {
		t.Fatal("ожидалась ошибка публикации части")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("монолит изменён: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "users_2.csv")); !os.IsNotExist(err) {
		t.Fatalf("новая часть users_2.csv не убрана: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "users_3.csv")); err != nil {
		t.Fatalf("лежавшая до прогона users_3.csv уничтожена: %v", err)
	}
	assertNoTemps(t, dir)
}

// Незакрытая кавычка в чужом CSV превращала остаток файла в одну запись
// в памяти. Запись длиннее предела — ошибка нарезки, файл не тронут.
func TestSplitRejectsOverlongRecord(t *testing.T) {
	prev := splitMaxRecord
	splitMaxRecord = 64
	defer func() { splitMaxRecord = prev }()

	dir := t.TempDir()
	path := filepath.Join(dir, "t.csv")
	body := "\"h\"\n\"open\n" + strings.Repeat("\"a\"\n", 100)
	writeRaw(t, path, body)

	if _, err := splitFile(path, SplitWithHeader, 1, 1, nil); err == nil {
		t.Fatal("ожидалась ошибка длины записи")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != body {
		t.Fatalf("файл изменён: err=%v", err)
	}
	assertNoTemps(t, dir)
}

func TestSplitQuotedNewlineStaysOneRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.csv")
	writeRaw(t, path, "\"a\nb\"\n\"c\"\n")

	res, err := splitFile(path, SplitNoHeader, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertPartBytes(t, res.Parts, []string{
		"\"a\nb\"\n",
		"\"c\"\n",
	})
}

func TestSplitDropsOnlyTrailingEmptyWhenCutting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.csv")
	writeRaw(t, path, "\"h\"\n\n\"a\"\n\n")

	res, err := splitFile(path, SplitWithHeader, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertPartBytes(t, res.Parts, []string{
		"\"h\"\n\n",
		"\"h\"\n\"a\"\n",
	})
}

func TestSplitRejectsConvertedAndNonCSV(t *testing.T) {
	dir := t.TempDir()
	cases := []string{
		"converted.txt",
		"Converted.TXT",
		"notes.txt",
		".2csv-abc.tmp",
	}
	for _, name := range cases {
		path := filepath.Join(dir, name)
		writeRaw(t, path, "\"a\"\n\"b\"\n")
		before := fileSHA(t, path)
		if _, err := SplitIfNeeded(path, SplitWithHeader); err == nil {
			t.Fatalf("%s принят", name)
		}
		if fileSHA(t, path) != before {
			t.Fatalf("%s изменён", name)
		}
	}

	csvPath := filepath.Join(dir, "DATA.CSV")
	writeRaw(t, csvPath, "\"h\"\n\"a\"\n")
	before := fileSHA(t, csvPath)
	res, err := SplitIfNeeded(csvPath, SplitWithHeader)
	if err != nil {
		t.Fatal(err)
	}
	if res.Split || res.DataRows != 1 || fileSHA(t, csvPath) != before {
		t.Fatalf("DATA.CSV: %+v", res)
	}
}

func TestCommitRecordsHeaderMode(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()

	withHeader, err := Create(reg, dir, "users", []string{"email"})
	if err != nil {
		t.Fatal(err)
	}
	if err := withHeader.Row([]string{"a@example.test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := withHeader.Commit(); err != nil {
		t.Fatal(err)
	}

	noHeader, err := Create(reg, dir, "plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := noHeader.Row([]string{"x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := noHeader.Commit(); err != nil {
		t.Fatal(err)
	}

	plain, err := CreatePlain(reg, dir, "book_sheet", []string{"h"})
	if err != nil {
		t.Fatal(err)
	}
	if err := plain.Row([]string{"v"}); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Commit(); err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, out := range reg.OutputsIn(dir) {
		got[filepath.Base(out.Path)] = out.HasHeader
	}
	if got["users.csv"] != true || got["plain.csv"] != false || got["book_sheet.csv"] != true {
		t.Fatalf("шапки: %#v", got)
	}
}

func TestLimitCSVBaseSuffixKeepsNumber(t *testing.T) {
	if got := limitCSVBaseSuffix("users", "_2"); got != "users_2" {
		t.Fatalf("короткое имя: %q", got)
	}
	base := strings.Repeat("я", 250)
	got := limitCSVBaseSuffix(base, "_2")
	if utf16Units(got) > maxCSVBaseUTF16 {
		t.Fatalf("длина %d", utf16Units(got))
	}
	if !strings.HasSuffix(got, "_2") || !strings.Contains(got, "~") {
		t.Fatalf("суффикс: %q", got)
	}
}

func writePlainRows(t testing.TB, path, header, line string, rows int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	if header != "" {
		if _, err := w.WriteString(header); err != nil {
			t.Fatal(err)
		}
	}
	b := []byte(line)
	for range rows {
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

func writeRaw(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := bufio.NewReader(f).WriteTo(h); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func readHead(t *testing.T, path string, n int) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, n)
	if _, err := f.Read(buf); err != nil {
		t.Fatal(err)
	}
	return buf
}

func countDataRows(path string, mode HeaderMode) (int64, error) {
	n, _, err := countDataRowsUntil(path, mode, -1)
	return n, err
}

func assertPartData(t *testing.T, parts []string, mode HeaderMode, want []int64) {
	t.Helper()
	if len(parts) != len(want) {
		t.Fatalf("частей %d, ожидалось %d (%v)", len(parts), len(want), parts)
	}
	for i, p := range parts {
		n, err := countDataRows(p, mode)
		if err != nil {
			t.Fatal(err)
		}
		if n != want[i] {
			t.Fatalf("%s: строк %d, ожидалось %d", p, n, want[i])
		}
		if n > SplitChunkRows {
			t.Fatalf("%s: кусок больше %d", p, SplitChunkRows)
		}
	}
}

func assertPartBytes(t *testing.T, parts, want []string) {
	t.Helper()
	if len(parts) != len(want) {
		t.Fatalf("частей %d, ожидалось %d", len(parts), len(want))
	}
	for i, p := range parts {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want[i] {
			t.Fatalf("%s:\n got %q\nwant %q", p, got, want[i])
		}
	}
}

func assertNoTemps(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".2csv-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("временные файлы: %v", matches)
	}
}
