package app

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"sql2csv/internal/csvout"
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

func benchmarkWriteRows(b *testing.B, path, header, row string, rows int) {
	b.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 256*1024)
	if _, err := w.WriteString(header); err != nil {
		b.Fatal(err)
	}
	for range rows {
		if _, err := w.WriteString(row); err != nil {
			b.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		b.Fatal(err)
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}
}

func benchmarkRunFixture(b *testing.B, fixture string, wantCSV int) {
	b.Helper()
	var inputBytes int64
	if err := filepath.Walk(fixture, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			inputBytes += info.Size()
		}
		return err
	}); err != nil {
		b.Fatal(err)
	}

	runRoot := filepath.Join(b.TempDir(), "run")
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
		if res.CSV != wantCSV || res.FilesFail != 0 {
			b.Fatalf("%+v", res)
		}
	}
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
	benchmarkRunFixture(b, fixture, 6)
}

func BenchmarkRunParallelInputs(b *testing.B) {
	b.Run("sql", func(b *testing.B) {
		fixture := filepath.Join(b.TempDir(), "fixture")
		data := []byte(benchmarkInsert(50_000))
		for i := range 16 {
			path := filepath.Join(fixture, "sql-"+strconv.Itoa(i), "dump.sql")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				b.Fatal(err)
			}
		}
		benchmarkRunFixture(b, fixture, 16)
	})

	b.Run("xlsx", func(b *testing.B) {
		fixture := filepath.Join(b.TempDir(), "fixture")
		data := benchmarkSheet(20_000)
		for i := range 16 {
			path := filepath.Join(fixture, "xlsx-"+strconv.Itoa(i), "book.xlsx")
			if err := xlsconv.WriteXLSX(path, []xlsconv.Sheet{{Name: "Data", Rows: data}}); err != nil {
				b.Fatal(err)
			}
		}
		benchmarkRunFixture(b, fixture, 16)
	})

	b.Run("xls", func(b *testing.B) {
		fixture := filepath.Join(b.TempDir(), "fixture")
		data := make([][]string, 20_000)
		data[0] = []string{"id", "email", "name"}
		for row := 1; row < len(data); row++ {
			data[row] = []string{"1", "a@example.test", "Name"}
		}
		for i := range 8 {
			path := filepath.Join(fixture, "xls-"+strconv.Itoa(i), "book.xls")
			if err := xlsconv.WriteXLS(path, []xlsconv.Sheet{{Name: "Data", Rows: data}}); err != nil {
				b.Fatal(err)
			}
		}
		benchmarkRunFixture(b, fixture, 8)
	})

	for _, c := range []struct {
		name, ext, header, row string
	}{
		{"csv", ".csv", "\"id\"\n", "\"1\"\n"},
		{"txt", ".txt", "", "1\n"},
	} {
		b.Run("split-"+c.name, func(b *testing.B) {
			fixture := filepath.Join(b.TempDir(), "fixture")
			for i := range 8 {
				path := filepath.Join(fixture, c.name+"-"+strconv.Itoa(i), "big"+c.ext)
				benchmarkWriteRows(b, path, c.header, c.row, csvout.SplitThreshold+1)
			}
			benchmarkRunFixture(b, fixture, 8)
		})
	}
}
