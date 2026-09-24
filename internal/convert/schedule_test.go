package convert

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sql2csv/internal/csvout"
	"sql2csv/internal/logx"
	"sql2csv/internal/scan"
)

func TestScheduleKeepsInsertOrderWhenLaterParseFinishesFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.sql")
	sql := "INSERT INTO users (email) VALUES ('first');\nINSERT INTO users (email) VALUES ('second');\n"
	if err := os.WriteFile(path, []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}
	var n atomic.Int32
	submit := func(fn func()) {
		id := n.Add(1)
		go func() {
			if id == 1 {
				time.Sleep(40 * time.Millisecond)
			}
			fn()
		}()
	}
	res := Schedule(logx.New(&discardLog{}), csvout.NewRegistry(), scan.SQLFile{Path: path}, submit)
	if res.Failed || res.Created != 2 || res.CSV != 1 {
		t.Fatalf("%+v", res)
	}
	got, err := os.ReadFile(filepath.Join(dir, "users.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := "\"email\"\n\"first\"\n\"second\"\n"
	if string(got) != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
	}
}

// §6: арность строки VALUES сверяется с шириной ключа (шапка или первая
// строка первого успешного INSERT), а не со списком колонок текущего INSERT.
func TestScheduleRowWidthFollowsKeyWidth(t *testing.T) {
	tests := []struct {
		name        string
		sql         string
		want        string
		wantCreated int
	}{
		{
			name: "список колонок длиннее ключа, значений не больше ключа",
			sql: "INSERT INTO users VALUES ('a','b');\n" +
				"INSERT INTO users (c1, c2, c3) VALUES ('x','y');\n",
			want:        "\"a\",\"b\"\n\"x\",\"y\"\n",
			wantCreated: 2,
		},
		{
			name:        "короткие строки дополняются до шапки",
			sql:         "INSERT INTO users (c1, c2, c3) VALUES ('1','2'),('1','2','3');\n",
			want:        "\"c1\",\"c2\",\"c3\"\n\"1\",\"2\",\"\"\n\"1\",\"2\",\"3\"\n",
			wantCreated: 1,
		},
		{
			name: "без колонок: строка шире ключа пропускается одна (PR #6)",
			sql: "INSERT INTO users VALUES ('a','b');\n" +
				"INSERT INTO users VALUES ('c','d'),('e','f','g');\n",
			want:        "\"a\",\"b\"\n\"c\",\"d\"\n",
			wantCreated: 2,
		},
		{
			name: "значений больше ширины ключа",
			sql: "INSERT INTO users (c1) VALUES ('a');\n" +
				"INSERT INTO users (c1, c2) VALUES ('x','y');\n",
			want:        "\"c1\"\n\"a\"\n",
			wantCreated: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "a.sql")
			if err := os.WriteFile(path, []byte(tt.sql), 0o644); err != nil {
				t.Fatal(err)
			}
			res := Schedule(logx.New(&discardLog{}), csvout.NewRegistry(), scan.SQLFile{Path: path}, nil)
			if res.Created != tt.wantCreated {
				t.Fatalf("Created=%d, want %d: %+v", res.Created, tt.wantCreated, res)
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

// INSERT больше лимита памяти DataFile уходит во временный файл; строки
// короче шапки дополняются при повторном чтении. Temp-файлов не остаётся.
func TestScheduleLargeInsertSpillsAndPads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.sql")
	const rows = 40_000 // ≈ 2 МБ строк: больше dataMemLimit
	var sql strings.Builder
	sql.WriteString("INSERT INTO users (id, email, name) VALUES ")
	for i := range rows {
		if i > 0 {
			sql.WriteByte(',')
		}
		n := strconv.Itoa(i)
		sql.WriteString("(" + n + ",'user" + n + "@example.test')")
	}
	sql.WriteString(";\n")
	if err := os.WriteFile(path, []byte(sql.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Schedule(logx.New(&discardLog{}), csvout.NewRegistry(), scan.SQLFile{Path: path}, nil)
	if res.Failed || res.Created != 1 {
		t.Fatalf("%+v", res)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "users.csv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != rows+1 {
		t.Fatalf("строк %d, ожидалось %d", len(lines), rows+1)
	}
	if lines[rows] != "\"39999\",\"user39999@example.test\",\"\"" {
		t.Fatalf("последняя строка: %q", lines[rows])
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".2csv-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("временные файлы: %v", matches)
	}
}

func TestScheduleGrantInsertIsQuiet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.sql")
	sql := "GRANT INSERT, UPDATE ON users TO app;\nINSERT INTO users (email) VALUES ('a');\n"
	if err := os.WriteFile(path, []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	res := Schedule(logx.New(&buf), csvout.NewRegistry(), scan.SQLFile{Path: path}, nil)
	if res.Created != 1 || res.UnitFail != 0 || buf.Len() != 0 {
		t.Fatalf("GRANT INSERT не ошибка: %+v log=%q", res, buf.String())
	}
}

// Файл со скриншота: одна строка INSERT шире списка колонок. Раньше весь
// INSERT на ~190 строк отбрасывался и CSV не было. Теперь пропадает одна
// строка (error со счётчиком), остальные в CSV; исходник удалять нельзя —
// пропущенная строка есть только в нём (проверяет app).
func TestScheduleOneWideRowKeepsRestOfInsert(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "interestedtoparticipate.sql")
	sql := "INSERT INTO `interestedtoparticipate` (`id`, `email`, `mobile`) VALUES\n" +
		"(1, 'a@example.test', '1'),\n" +
		"(2, 'b@example.test', '2', 'EXTRA'),\n" +
		"(3, 'c@example.test', '3');\n"
	if err := os.WriteFile(path, []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	res := Schedule(logx.New(&buf), csvout.NewRegistry(), scan.SQLFile{Path: path}, nil)
	if res.Created != 1 || res.CSV != 1 || res.UnitFail != 1 {
		t.Fatalf("%+v log=%q", res, buf.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "interestedtoparticipate.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := "\"id\",\"email\",\"mobile\"\n\"1\",\"a@example.test\",\"1\"\n\"3\",\"c@example.test\",\"3\"\n"
	if string(got) != want {
		t.Fatalf("CSV:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(buf.String(), "1 строк пропущено") || strings.Contains(buf.String(), "EXTRA") {
		t.Fatalf("лог: %q", buf.String())
	}
}

type discardLog struct{}

func (discardLog) Write(p []byte) (int, error) { return len(p), nil }
