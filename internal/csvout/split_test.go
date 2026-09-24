package csvout

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

	res, err := splitFile(path, SplitWithHeader, 2, 2)
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

	res, err := splitFile(path, SplitNoHeader, 2, 2)
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

	res, err := splitFile(path, SplitWithHeader, 1, 1)
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

func TestSplitSkipsOccupiedPartName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.csv")
	writeRaw(t, path, "\"a\"\n\"b\"\n\"c\"\n")
	occupied := filepath.Join(dir, "users_2.csv")
	writeRaw(t, occupied, "KEEP\n")

	res, err := splitFile(path, SplitNoHeader, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertPartBytes(t, res.Parts, []string{
		"\"a\"\n\"b\"\n",
		"\"c\"\n",
	})
	if filepath.Base(res.Parts[1]) != "users_3.csv" {
		t.Fatalf("часть: %s", res.Parts[1])
	}
	got, err := os.ReadFile(occupied)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "KEEP\n" {
		t.Fatalf("занятый файл изменён: %q", got)
	}
}

func TestSplitPartFileUsesNextNumber(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users_2.csv")
	writeRaw(t, path, "\"a\"\n\"b\"\n\"c\"\n")

	res, err := splitFile(path, SplitNoHeader, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertPartBytes(t, res.Parts, []string{
		"\"a\"\n\"b\"\n",
		"\"c\"\n",
	})
	if filepath.Base(res.Parts[0]) != "users_2.csv" || filepath.Base(res.Parts[1]) != "users_3.csv" {
		t.Fatalf("имена: %v", res.Parts)
	}
}

func TestSplitQuotedNewlineStaysOneRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.csv")
	writeRaw(t, path, "\"a\nb\"\n\"c\"\n")

	res, err := splitFile(path, SplitNoHeader, 1, 1)
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

	res, err := splitFile(path, SplitWithHeader, 1, 1)
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

func TestSplitRecordTooLargeKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rows.csv")
	restore := SetSplitLimits(2, 2)
	defer restore()
	restoreRec := SetMaxRecordBytes(64)
	defer restoreRec()

	var b strings.Builder
	b.WriteString("\"h\"\n\"")
	for i := 0; i < 200; i++ {
		b.WriteByte('x')
	}
	b.WriteString("\"\n\"a\"\n\"b\"\n\"c\"\n")
	writeRaw(t, path, b.String())
	before := fileSHA(t, path)
	_, err := SplitIfNeeded(path, SplitWithHeader)
	if err == nil || !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("ожидался ErrRecordTooLarge, got %v", err)
	}
	if fileSHA(t, path) != before {
		t.Fatal("при ошибке нарезки монолит должен остаться")
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

func TestSuffixStart(t *testing.T) {
	cases := []struct {
		stem string
		base string
		n    int
	}{
		{"users", "users", 2},
		{"users_2", "users", 3},
		{"users_2_9", "users_2", 10},
		{"users_02", "users_02", 2},
		{"_2", "_2", 2},
	}
	for _, c := range cases {
		base, n := suffixStart(c.stem)
		if base != c.base || n != c.n {
			t.Fatalf("%s: %s %d", c.stem, base, n)
		}
	}
}

func writePlainRows(t *testing.T, path, header, line string, rows int) {
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
	for i := 0; i < rows; i++ {
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
