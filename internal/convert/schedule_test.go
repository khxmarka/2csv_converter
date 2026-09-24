package convert

import (
	"os"
	"path/filepath"
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
			name: "без колонок: строка длиннее первой отбрасывает INSERT",
			sql: "INSERT INTO users VALUES ('a','b');\n" +
				"INSERT INTO users VALUES ('c','d'),('e','f','g');\n",
			want:        "\"a\",\"b\"\n",
			wantCreated: 1,
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

type discardLog struct{}

func (discardLog) Write(p []byte) (int, error) { return len(p), nil }
