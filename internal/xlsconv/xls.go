package xlsconv

import (
	"fmt"
	"os"

	"github.com/nkiri/xls"
)

func readXLS(path string) (Book, error) {
	wb, tooMany, err := openXLS(path)
	if err != nil {
		return Book{}, err
	}
	if tooMany {
		return Book{SkipTooMany: true}, nil
	}

	sheets := make([]Sheet, 0, wb.SheetCount())
	for i := 0; i < wb.SheetCount(); i++ {
		sh := wb.Sheet(i)
		if sh == nil {
			continue
		}
		sheets = append(sheets, makeSheet(sh.Name(), sh.Strings()))
	}
	return Book{Sheets: sheets}, nil
}

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
