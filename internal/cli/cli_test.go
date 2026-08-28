package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("--help должен давать код %d, получено %d", exitOK, code)
	}
	if !strings.Contains(stdout.String(), "sql2csv") {
		t.Fatalf("справка должна печататься в stdout, получено: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("при --help stderr должен быть пуст, получено: %q", stderr.String())
	}
}

func TestRunUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"--нет-такого-флага"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("неизвестный флаг должен давать код %d, получено %d", exitUsage, code)
	}
	if stderr.Len() == 0 {
		t.Fatal("причина ошибки должна уходить в stderr")
	}
}

func TestRunExtraArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"C:\\other\\path"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("лишний аргумент должен давать код %d, получено %d", exitUsage, code)
	}
	if !strings.Contains(stderr.String(), "лишние аргументы") {
		t.Fatalf("stderr должен объяснять причину, получено: %q", stderr.String())
	}
}
