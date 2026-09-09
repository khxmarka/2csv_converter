package converted

import (
	"bytes"
	"os"
	"runtime"
	"strconv"
	"sync"
	"testing"
)

func TestReadMissingReturnsEmptySet(t *testing.T) {
	got, err := Read(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("набор=%v", got)
	}
}

func TestAppendAccumulatesWithoutDuplicates(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Alpha", "Beta", "Alpha"} {
		if err := Append(root, name); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(Path(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "Alpha\nBeta\n" {
		t.Fatalf("converted.txt=%q", raw)
	}
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("BOM запрещён")
	}
	done, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 2 {
		t.Fatalf("набор=%v", done)
	}
	if _, ok := done["Alpha"]; !ok {
		t.Fatal("нет Alpha")
	}
	if _, ok := done["Beta"]; !ok {
		t.Fatal("нет Beta")
	}
}

func TestIncompleteFinalLineIsIgnoredAndRemovedBeforeAppend(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(Path(root), []byte("OLD\nPARTIAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	done, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 1 {
		t.Fatalf("незавершённая строка не должна считаться готовой: %v", done)
	}
	if _, ok := done["OLD"]; !ok {
		t.Fatalf("нет завершённой строки OLD: %v", done)
	}
	if err := Append(root, "Alpha"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(Path(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "OLD\nAlpha\n" {
		t.Fatalf("незавершённый хвост не удалён безопасно: %q", raw)
	}
}

func TestReadAcceptsBOMAndCRLF(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(Path(root), []byte("\xEF\xBB\xBFAlpha\r\nBeta\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	done, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 2 {
		t.Fatalf("набор=%v", done)
	}
	if _, ok := done["Alpha"]; !ok {
		t.Fatal("нет Alpha")
	}
	if _, ok := done["Beta"]; !ok {
		t.Fatal("нет Beta")
	}
}

func TestAppendRejectsInvalidName(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"", "A\nB", "A\rB"} {
		if err := Append(root, name); err == nil {
			t.Fatalf("Append(%q) должен вернуть ошибку", name)
		}
	}
	if _, err := os.Stat(Path(root)); !os.IsNotExist(err) {
		t.Fatalf("converted.txt не должен создаваться, err=%v", err)
	}
}

func TestAppendConcurrentNamesRemainWholeAndUnique(t *testing.T) {
	root := t.TempDir()
	const unique = 20
	const repeats = 5
	var wg sync.WaitGroup
	errs := make(chan error, unique*repeats)
	for i := 0; i < unique*repeats; i++ {
		name := "Folder " + strconv.Itoa(i%unique)
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- Append(root, name)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	done, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != unique {
		t.Fatalf("уникальных имён=%d, ожидалось %d: %v", len(done), unique, done)
	}
	raw, err := os.ReadFile(Path(root))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(raw, []byte{'\n'}) != unique {
		t.Fatalf("повреждённые/дублированные строки: %q", raw)
	}
}

func TestAppendDuplicateNameIsCaseInsensitiveOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows filesystem semantics")
	}
	root := t.TempDir()
	if err := Append(root, "Alpha"); err != nil {
		t.Fatal(err)
	}
	if err := Append(root, "alpha"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(Path(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "Alpha\n" {
		t.Fatalf("дубликат с другим регистром: %q", raw)
	}
}
