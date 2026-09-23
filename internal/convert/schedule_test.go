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

type discardLog struct{}

func (discardLog) Write(p []byte) (int, error) { return len(p), nil }
