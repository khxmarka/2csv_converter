package xlsconv

import (
	"github.com/nkiri/xls"
)

func readXLS(path string) (Book, error) {
	wb, err := xls.Open(path)
	if err != nil {
		return Book{}, err
	}
	if wb.SheetCount() > MaxSheets {
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
