package csvout

import (
	"path/filepath"
	"testing"
)

// Подсчёт строк идёт по каждому CSV незавершённой папки на каждом запуске,
// нарезка — по файлам больше порога.
func BenchmarkSplit(b *testing.B) {
	path := filepath.Join(b.TempDir(), "big.csv")
	const rows = 300_000
	writePlainRows(b, path, "\"id\",\"email\"\n", "\"1\",\"user@example.test, \"\"quoted\"\"\"\n", rows)
	b.Run("count", func(b *testing.B) {
		for b.Loop() {
			if _, over, err := countDataRowsUntil(path, SplitWithHeader, rows); err != nil || over {
				b.Fatal(err)
			}
		}
	})
}
