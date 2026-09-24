package convert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLPreambleIsNotTabularDump(t *testing.T) {
	const insert = "INSERT INTO users (email, phone) VALUES ('a@example.test', '555');\n"
	tests := []struct {
		name     string
		preamble string
	}{
		{"sqlite pragma", "PRAGMA foreign_keys=OFF;\nBEGIN TRANSACTION;\n"},
		{"mysql start transaction", "START TRANSACTION;\n"},
		{"commit", "COMMIT;\n"},
		{"rollback", "ROLLBACK;\n"},
		{"mssql declare", "DECLARE @a INT, @b INT;\n"},
		{"mssql if", "IF OBJECT_ID('users', 'U') IS NOT NULL DROP TABLE users;\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			res, log := runFile(t, dir, "users.sql", tt.preamble+insert)
			if strings.Contains(log, "не INSERT, а таблица") {
				t.Fatalf("SQL-преамбула принята за шапку:\n%s", log)
			}
			if res.Created != 1 || res.CSV != 1 {
				t.Fatalf("INSERT потерян: %+v log=%q", res, log)
			}
			got, err := os.ReadFile(filepath.Join(dir, "users.csv"))
			if err != nil {
				t.Fatal(err)
			}
			want := "\"email\",\"phone\"\n\"a@example.test\",\"555\"\n"
			if string(got) != want {
				t.Fatalf("CSV:\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestSniffTableStopsAtBufferOnHugeFirstLine(t *testing.T) {
	var sql strings.Builder
	sql.WriteString("INSERT INTO users (email, phone) VALUES ")
	for i := 0; sql.Len() <= 2*tableReaderSize; i++ {
		if i > 0 {
			sql.WriteByte(',')
		}
		sql.WriteString("('a@example.test','555')")
	}
	sql.WriteByte(';') // без перевода строки: весь дамп — одна строка

	dir := t.TempDir()
	path := filepath.Join(dir, "users.sql")
	if err := os.WriteFile(path, []byte(sql.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h, _, err := sniffTable(f)
	if err != nil || h != nil {
		t.Fatalf("строка длиннее буфера не шапка: h=%v err=%v", h, err)
	}

	res, log := runFile(t, dir, "users.sql", sql.String())
	if res.Created != 1 || res.CSV != 1 {
		t.Fatalf("однострочный INSERT потерян: %+v log=%q", res, log)
	}
}

func TestTabularHeaderEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "первая колонка совпадает с SQL-словом",
			raw:  "desc,email,phone\nx,a@example.test,555\n",
			want: "\"desc\",\"email\",\"phone\"\n\"x\",\"a@example.test\",\"555\"\n",
		},
		{
			name: "колонка начинается со слова оператора",
			raw:  "Start Date;Commit Hash;email\n2024;abc;a@example.test\n",
			want: "\"Start Date\",\"Commit Hash\",\"email\"\n\"2024\",\"abc\",\"a@example.test\"\n",
		},
		{
			name: "пустое имя индекса pandas",
			raw:  ",email,phone\n0,a@example.test,555\n",
			want: "\"\",\"email\",\"phone\"\n\"0\",\"a@example.test\",\"555\"\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			res, log := runFile(t, dir, "users.sql", tt.raw)
			if res.CSV != 1 {
				t.Fatalf("таблица не распознана: %+v log=%q", res, log)
			}
			got, err := os.ReadFile(filepath.Join(dir, "users.csv"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("CSV:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}
