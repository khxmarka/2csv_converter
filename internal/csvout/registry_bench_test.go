package csvout

import (
	"path/filepath"
	"strconv"
	"testing"
)

// Большой корень: тысячи директорий по десятку CSV. addOutput и OutputsIn
// вызываются на каждый CSV и на каждую директорию.
func BenchmarkRegistryOutputs(b *testing.B) {
	const dirs, perDir = 1000, 20
	root := b.TempDir()
	paths := make([][]string, dirs)
	for d := range dirs {
		dir := filepath.Join(root, "d"+strconv.Itoa(d))
		for i := range perDir {
			paths[d] = append(paths[d], filepath.Join(dir, "t"+strconv.Itoa(i)+".csv"))
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		reg := NewRegistry()
		for d := range dirs {
			dir := filepath.Dir(paths[d][0])
			for _, p := range paths[d] {
				reg.addOutput(dir, p, true)
			}
			if got := len(reg.OutputsIn(dir)); got != perDir {
				b.Fatalf("OutputsIn=%d", got)
			}
		}
	}
}
