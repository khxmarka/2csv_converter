// Package xlsconv читает листы Excel (.xlsx / .xls) и пишет CSV рядом с книгой (§13).
package xlsconv

import "errors"

const (
	// MaxSheets skips workbooks with more sheets than this limit.
	MaxSheets = 5
	// MaxXLSBytes bounds memory use of the non-streaming BIFF reader.
	MaxXLSBytes int64 = 256 << 20
	// MaxXLSXUnpackedBytes limits decompressed workbook data.
	MaxXLSXUnpackedBytes int64 = 4 << 30
	// MaxXLSXXMLMemoryBytes spills larger worksheet XML to temporary files.
	MaxXLSXXMLMemoryBytes int64 = 16 << 20
)

var errNeedRegistry = errors.New("xlsconv: нужен Registry")

// Sheet describes one worksheet for test data generation.
type Sheet struct {
	Name string
	Rows [][]string
}
