package xlsconv

import (
	"fmt"
	"os"
	"sync"

	"github.com/nkiri/xls"
)

var xlsMemoryMu sync.Mutex

func openXLS(path string) (*xls.Workbook, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}
	if info.Size() > MaxXLSBytes {
		return nil, false, fmt.Errorf("xlsconv: .xls больше %d MiB", MaxXLSBytes>>20)
	}
	wb, err := xls.Open(path)
	if err != nil {
		return nil, false, err
	}
	return wb, wb.SheetCount() > MaxSheets, nil
}
