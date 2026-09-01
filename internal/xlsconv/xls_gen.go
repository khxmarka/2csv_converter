package xlsconv

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf16"
)

const (
	recBOF        uint16 = 0x0809
	recEOF        uint16 = 0x000A
	recBoundSheet uint16 = 0x0085
	recSST        uint16 = 0x00FC
	recXF         uint16 = 0x00E0
	recLabelSST   uint16 = 0x00FD
	bofWorkbook   uint16 = 0x0005
	bofSheet      uint16 = 0x0010
)

// WriteXLS пишет минимальную BIFF8-книгу (для тестов и фикстур, не для конвертера).
func WriteXLS(path string, sheets []Sheet) error {
	if len(sheets) == 0 {
		return fmt.Errorf("xlsconv: нужен хотя бы один лист")
	}

	type cellRef struct {
		sheet, row, col, sst int
	}
	var sst []string
	var cells []cellRef
	for si, sh := range sheets {
		for r, row := range sh.Rows {
			for c, val := range row {
				if val == "" {
					continue
				}
				cells = append(cells, cellRef{si, r, c, len(sst)})
				sst = append(sst, val)
			}
		}
	}

	var glob bytes.Buffer
	writeRec(&glob, recBOF, bofPayload(bofWorkbook))
	writeRec(&glob, recXF, xfRecord(0))
	if len(sst) > 0 {
		writeRec(&glob, recSST, sstPayload(sst))
	}

	bsOff := make([]int, len(sheets))
	for i, sh := range sheets {
		bsOff[i] = glob.Len() + 4
		writeRec(&glob, recBoundSheet, boundSheetPayload(0, sh.Name))
	}
	writeRec(&glob, recEOF, nil)
	globBytes := glob.Bytes()

	sheetBlobs := make([][]byte, len(sheets))
	for i := range sheets {
		var sb bytes.Buffer
		writeRec(&sb, recBOF, bofPayload(bofSheet))
		for _, c := range cells {
			if c.sheet != i {
				continue
			}
			writeRec(&sb, recLabelSST, labelSSTPayload(c.row, c.col, c.sst))
		}
		writeRec(&sb, recEOF, nil)
		sheetBlobs[i] = sb.Bytes()
	}

	offset := uint32(len(globBytes))
	for i, blob := range sheetBlobs {
		binary.LittleEndian.PutUint32(globBytes[bsOff[i]:], offset)
		offset += uint32(len(blob))
	}

	var full []byte
	full = append(full, globBytes...)
	for _, blob := range sheetBlobs {
		full = append(full, blob...)
	}

	ole, err := wrapOLEWorkbook(full)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, ole, 0o644)
}

func writeRec(buf *bytes.Buffer, typ uint16, data []byte) {
	var hdr [4]byte
	binary.LittleEndian.PutUint16(hdr[0:2], typ)
	binary.LittleEndian.PutUint16(hdr[2:4], uint16(len(data)))
	buf.Write(hdr[:])
	buf.Write(data)
}

func bofPayload(kind uint16) []byte {
	var buf [16]byte
	binary.LittleEndian.PutUint16(buf[0:2], 0x0600)
	binary.LittleEndian.PutUint16(buf[2:4], kind)
	binary.LittleEndian.PutUint16(buf[4:6], 0x0DBB)
	binary.LittleEndian.PutUint16(buf[6:8], 0x07CC)
	binary.LittleEndian.PutUint32(buf[8:12], 0x00000041)
	binary.LittleEndian.PutUint32(buf[12:16], 0x00000006)
	return buf[:]
}

func xfRecord(fmtIdx uint16) []byte {
	var b [20]byte
	binary.LittleEndian.PutUint16(b[2:4], fmtIdx)
	binary.LittleEndian.PutUint16(b[4:6], 0xFFF5)
	return b[:]
}

func sstPayload(strs []string) []byte {
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(strs)))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(strs)))
	out := hdr[:]
	for _, s := range strs {
		out = append(out, encodeLongString(s)...)
	}
	return out
}

func boundSheetPayload(bofOffset uint32, name string) []byte {
	var hdr [6]byte
	binary.LittleEndian.PutUint32(hdr[0:4], bofOffset)
	hdr[4] = 0
	hdr[5] = 0
	return append(hdr[:], encodeShortString(name)...)
}

func labelSSTPayload(row, col, sstIdx int) []byte {
	var b [10]byte
	binary.LittleEndian.PutUint16(b[0:2], uint16(row))
	binary.LittleEndian.PutUint16(b[2:4], uint16(col))
	binary.LittleEndian.PutUint32(b[6:10], uint32(sstIdx))
	return b[:]
}

func encodeLongString(s string) []byte {
	runes := []rune(s)
	compressed := latin1(runes)
	size := 3 + len(runes)
	if !compressed {
		size = 3 + len(runes)*2
	}
	b := make([]byte, size)
	binary.LittleEndian.PutUint16(b[0:2], uint16(len(runes)))
	if !compressed {
		b[2] = 0x01
	}
	putChars(b[3:], runes, compressed)
	return b
}

func encodeShortString(s string) []byte {
	runes := []rune(s)
	if len(runes) > 255 {
		runes = runes[:255]
	}
	compressed := latin1(runes)
	size := 2 + len(runes)
	if !compressed {
		size = 2 + len(runes)*2
	}
	b := make([]byte, size)
	b[0] = byte(len(runes))
	if !compressed {
		b[1] = 0x01
	}
	putChars(b[2:], runes, compressed)
	return b
}

func latin1(rs []rune) bool {
	for _, r := range rs {
		if r > 0xFF {
			return false
		}
	}
	return true
}

func putChars(dst []byte, runes []rune, compressed bool) {
	if compressed {
		for i, r := range runes {
			dst[i] = byte(r)
		}
		return
	}
	words := utf16.Encode(runes)
	for i, w := range words {
		binary.LittleEndian.PutUint16(dst[i*2:], w)
	}
}
