package csvout

import (
	"os"
	"path/filepath"
	"testing"
)

// Counting runs for every CSV in an unfinished folder on every invocation.
func BenchmarkSplit(b *testing.B) {
	path := filepath.Join(b.TempDir(), "big.csv")
	const rows = 300_000
	writePlainRows(b, path, "\"id\",\"email\"\n", "\"1\",\"user@example.test, \"\"quoted\"\"\"\n", rows)
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("count", func(b *testing.B) {
		b.SetBytes(info.Size())
		b.ReportAllocs()
		for b.Loop() {
			if _, over, err := countDataRowsUntil(path, SplitWithHeader, rows); err != nil || over {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkSplitFiles(b *testing.B) {
	const rows = 300_000
	cases := []struct {
		name, ext, header, row string
		mode                   HeaderMode
	}{
		{"csv", ".csv", "\"id\",\"email\"\n", "\"1\",\"user@example.test, \"\"quoted\"\"\"\n", SplitWithHeader},
		{"txt", ".txt", "", "1:user@example.test\n", SplitLines},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			base := b.TempDir()
			runDir := filepath.Join(base, "run")
			if err := os.Mkdir(runDir, 0o755); err != nil {
				b.Fatal(err)
			}
			fixture := filepath.Join(base, "fixture"+c.ext)
			writePlainRows(b, fixture, c.header, c.row, rows)
			info, err := os.Stat(fixture)
			if err != nil {
				b.Fatal(err)
			}
			path := filepath.Join(runDir, "big"+c.ext)
			b.SetBytes(info.Size())
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				if err := os.Link(fixture, path); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				res, err := splitFile(path, c.mode, 100_000, 100_000, nil)
				if err != nil {
					b.Fatal(err)
				}
				if !res.Split || res.DataRows != rows || len(res.Parts) != 3 {
					b.Fatalf("%+v", res)
				}
				b.StopTimer()
				for _, part := range res.Parts {
					if err := os.Remove(part); err != nil {
						b.Fatal(err)
					}
				}
				b.StartTimer()
			}
		})
	}
}
