package converted

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEncodeEmpty(t *testing.T) {
	got := Encode(nil)
	if len(got) != 0 {
		t.Fatalf("пустой список должен давать пустые байты, получено %q", got)
	}
	if bytes.HasPrefix(got, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("BOM запрещён")
	}
}

func TestEncodeLFAndOrder(t *testing.T) {
	got := Encode([]string{"Alpha", "Beta"})
	want := "Alpha\nBeta\n"
	if string(got) != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestWriteOverwrites(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, FileName)
	if err := os.WriteFile(path, []byte("OLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(root, []string{"Alpha"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "Alpha\n" {
		t.Fatalf("не перезаписано: %q", raw)
	}
}
