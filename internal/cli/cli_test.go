package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	stdin := &blockingReader{t: t}

	if code := Run([]string{"--help"}, stdin, &stdout, &stderr); code != exitOK {
		t.Fatalf("--help должен давать код %d, получено %d", exitOK, code)
	}
	if !strings.Contains(stdout.String(), "2csv") {
		t.Fatalf("справка должна печататься в stdout, получено: %q", stdout.String())
	}
	for _, want := range []string{
		"PII", "двух колонках", "варианты (n) не создаются",
		"табличный дамп", "{stem}.csv", "папка в обработке", "хотя бы один CSV",
		"Пул воркеров", "500_000", "{имя}_2.csv", "уже лежавшие .csv / .txt", "удаляются",
		"_convert_done_.txt", "_convert_passed_.txt", "_splitter_done_.txt",
		"_splitter_passed_.txt", "_log.txt", "readme.txt",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("в справке нет %q: %q", want, stdout.String())
		}
	}
	if strings.Contains(stderr.String(), "combo/db:") {
		t.Fatalf("приглашение не должно печататься при --help, stderr: %q", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("при --help stderr должен быть пуст, получено: %q", stderr.String())
	}
	if stdin.reads != 0 {
		t.Fatal("--help не должен читать stdin")
	}
}

func TestRunStopsWhenPromptCannotBeWritten(t *testing.T) {
	stdin := &blockingReader{t: t}
	if code := Run(nil, stdin, io.Discard, failingWriter{}); code != exitFatal {
		t.Fatalf("код=%d, ожидался %d", code, exitFatal)
	}
	if stdin.reads != 0 {
		t.Fatal("после ошибки stderr нельзя читать stdin")
	}
}

func TestRunUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	stdin := &blockingReader{t: t}

	if code := Run([]string{"--нет-такого-флага"}, stdin, &stdout, &stderr); code != exitUsage {
		t.Fatalf("неизвестный флаг должен давать код %d, получено %d", exitUsage, code)
	}
	if stderr.Len() == 0 {
		t.Fatal("причина ошибки должна уходить в stderr")
	}
	if stdin.reads != 0 {
		t.Fatal("ошибка флага не должна читать stdin")
	}
}

func TestRunExtraArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	stdin := &blockingReader{t: t}

	if code := Run([]string{"C:\\other\\path"}, stdin, &stdout, &stderr); code != exitUsage {
		t.Fatalf("лишний аргумент должен давать код %d, получено %d", exitUsage, code)
	}
	if !strings.Contains(stderr.String(), "лишние аргументы") {
		t.Fatalf("stderr должен объяснять причину, получено: %q", stderr.String())
	}
	if stdin.reads != 0 {
		t.Fatal("лишние аргументы не должны читать stdin")
	}
}

func TestRunInvalidChoice(t *testing.T) {
	cases := []struct {
		name  string
		stdin io.Reader
	}{
		{name: "nope", stdin: strings.NewReader("nope\n")},
		{name: "empty", stdin: strings.NewReader("\n")},
		{name: "eof", stdin: strings.NewReader("")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(nil, tc.stdin, &stdout, &stderr); code != exitUsage {
				t.Fatalf("код %d, ожидался %d; stderr=%q", code, exitUsage, stderr.String())
			}
			if !strings.Contains(stderr.String(), "combo/db:") {
				t.Fatalf("должно быть приглашение, stderr: %q", stderr.String())
			}
			if !strings.Contains(stderr.String(), "ожидалось combo или db") {
				t.Fatalf("stderr должен объяснять причину, получено: %q", stderr.String())
			}
		})
	}
}

// blockingReader падает, если CLI читает stdin там, где не должен.
type blockingReader struct {
	t     *testing.T
	reads int
}

func (r *blockingReader) Read(p []byte) (int, error) {
	r.reads++
	r.t.Helper()
	r.t.Fatal("stdin не должен читаться")
	return 0, io.EOF
}
