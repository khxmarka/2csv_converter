package app

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"sql2csv/internal/logx"
	"sql2csv/internal/xlsconv"
)

func benchmarkInsert(rows int) string {
	var b strings.Builder
	b.WriteString("INSERT INTO users (id, email) VALUES ")
	for row := range rows {
		if row > 0 {
			b.WriteByte(',')
		}
		n := strconv.Itoa(row)
		b.WriteByte('(')
		b.WriteString(n)
		b.WriteString(",'user")
		b.WriteString(n)
		b.WriteString("@example.test')")
	}
	b.WriteString(";\n")
	return b.String()
}

func benchmarkSheet(rows int) [][]string {
	data := make([][]string, rows)
	data[0] = []string{"id", "email", "name"}
	for row := 1; row < rows; row++ {
		n := strconv.Itoa(row)
		data[row] = []string{n, "user" + n + "@example.test", "Name " + n}
	}
	return data
}

func BenchmarkRunConcurrentInputs(b *testing.B) {
	base := b.TempDir()
	fixture := filepath.Join(base, "fixture")
	for i := range 4 {
		dir := filepath.Join(fixture, "sql-"+strconv.Itoa(i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "dump.sql"), []byte(benchmarkInsert(5_000)), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	for i := range 2 {
		path := filepath.Join(fixture, "book-"+strconv.Itoa(i), "book.xlsx")
		if err := xlsconv.WriteXLSX(path, []xlsconv.Sheet{{Name: "Data", Rows: benchmarkSheet(2_000)}}); err != nil {
			b.Fatal(err)
		}
	}
	var inputBytes int64
	if err := filepath.Walk(fixture, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			inputBytes += info.Size()
		}
		return err
	}); err != nil {
		b.Fatal(err)
	}

	runRoot := filepath.Join(base, "run")
	log := logx.New(io.Discard)
	b.SetBytes(inputBytes)
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		if err := os.RemoveAll(runRoot); err != nil {
			b.Fatal(err)
		}
		if err := os.CopyFS(runRoot, os.DirFS(fixture)); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		res, err := Run(log, runRoot)
		if err != nil {
			b.Fatal(err)
		}
		if res.CSV != 6 || res.FilesFail != 0 {
			b.Fatalf("%+v", res)
		}
	}
}
