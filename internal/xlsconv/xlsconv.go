// Package xlsconv читает листы Excel (.xlsx / .xls) и пишет CSV рядом с книгой (§13).
package xlsconv

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

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

// Book — итог чтения. Если SkipTooMany, Sheets пуст и строк нет.
type Book struct {
	SkipTooMany bool
	Sheets      []Sheet
}

// Sheet — один лист книги. Empty — нет ни одной непустой ячейки после нормализации.
type Sheet struct {
	Name  string
	Rows  [][]string
	Empty bool
}

// Read читает .xls целиком в Book: у BIFF-библиотеки нет потокового чтения.
// .xlsx идёт потоково мимо Read (File → fileXLSX). Иное расширение — ошибка.
// Книга с более чем MaxSheets листами: SkipTooMany, без данных листов.
func Read(path string) (Book, error) {
	if strings.EqualFold(filepath.Ext(path), ".xls") {
		return readXLS(path)
	}
	ext := filepath.Ext(path)
	if ext == "" {
		ext = "(нет расширения)"
	}
	return Book{}, fmt.Errorf("xlsconv: неподдерживаемое расширение %s", ext)
}
