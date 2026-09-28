package convert

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"sql2csv/internal/csvout"
	"sql2csv/internal/logx"
	"sql2csv/internal/scan"
)

// benchPool matches the worker pool used by app.
func benchPool() (submit func(func()), stop func()) {
	jobs := make(chan func())
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for fn := range jobs {
				fn()
			}
		})
	}
	return func(fn func()) { jobs <- fn }, func() { close(jobs); wg.Wait() }
}

func benchTabularSQL(rows, columns int) string {
	var b strings.Builder
	b.WriteString("email,phone")
	for col := 2; col < columns; col++ {
		b.WriteString(",field_")
		b.WriteString(strconv.Itoa(col))
	}
	b.WriteByte('\n')
	for row := range rows {
		n := strconv.Itoa(row)
		b.WriteString("user")
		b.WriteString(n)
		b.WriteString("@example.test,555")
		b.WriteString(n)
		for col := 2; col < columns; col++ {
			b.WriteString(",value_")
			b.WriteString(n)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func benchSQL(inserts, rowsPerInsert int) string {
	var b strings.Builder
	for i := range inserts {
		b.WriteString("INSERT INTO users (id, email, name) VALUES ")
		for r := range rowsPerInsert {
			if r > 0 {
				b.WriteByte(',')
			}
			n := strconv.Itoa(i*rowsPerInsert + r)
			b.WriteString("(" + n + ",'user" + n + "@example.test','Name " + n + "')")
		}
		b.WriteString(";\n")
	}
	return b.String()
}

func BenchmarkScheduleTabular(b *testing.B) {
	cases := []struct {
		name          string
		rows, columns int
	}{
		{"long=100000x4", 100_000, 4},
		{"wide=20000x64", 20_000, 64},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			dir := b.TempDir()
			path := filepath.Join(dir, "users.sql")
			table := benchTabularSQL(c.rows, c.columns)
			if err := os.WriteFile(path, []byte(table), 0o644); err != nil {
				b.Fatal(err)
			}
			output := filepath.Join(dir, "users.csv")
			log := logx.New(&discardLog{})
			b.SetBytes(int64(len(table)))
			b.ReportAllocs()
			for b.Loop() {
				res := Schedule(log, csvout.NewRegistry(), scan.SQLFile{Path: path}, nil)
				if res.CSV != 1 || res.Failed {
					b.Fatalf("%+v", res)
				}
				b.StopTimer()
				if err := os.Remove(output); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
			}
		})
	}
}

func BenchmarkSchedule(b *testing.B) {
	cases := []struct {
		name          string
		inserts, rows int
	}{
		{"one-row-inserts=2000", 2000, 1},
		{"one-insert-rows=50000", 1, 50_000},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			dir := b.TempDir()
			path := filepath.Join(dir, "dump.sql")
			sql := benchSQL(c.inserts, c.rows)
			if err := os.WriteFile(path, []byte(sql), 0o644); err != nil {
				b.Fatal(err)
			}
			submit, stop := benchPool()
			defer stop()
			log := logx.New(&discardLog{})
			b.SetBytes(int64(len(sql)))
			b.ReportAllocs()
			for b.Loop() {
				res := Schedule(log, csvout.NewRegistry(), scan.SQLFile{Path: path}, submit)
				if res.Created != c.inserts {
					b.Fatalf("%+v", res)
				}
			}
		})
	}
}
