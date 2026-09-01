package xlsconv

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

// wrapOLEWorkbook упаковывает BIFF8-поток в OLE2 с единственным потоком Workbook.
// Нужен только тестам: продакшен свой BIFF/OLE не пишет.
func wrapOLEWorkbook(biffData []byte) ([]byte, error) {
	w := oleWriter{}
	w.add("Workbook", biffData)
	var buf bytes.Buffer
	if err := w.writeTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const (
	oleFree      uint32 = 0xFFFFFFFF
	oleEnd       uint32 = 0xFFFFFFFE
	oleFAT       uint32 = 0xFFFFFFFD
	oleSector           = 512
	oleMini             = 64
	oleCutoff    uint32 = 4096
	oleHeader           = 512
	oleDirEntry         = 128
	oleObjStream byte   = 2
	oleObjRoot   byte   = 5
)

var oleMagic = [8]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

type oleWriter struct {
	streams []oleStream
}

type oleStream struct {
	name string
	data []byte
}

func (w *oleWriter) add(name string, data []byte) {
	w.streams = append(w.streams, oleStream{name: name, data: data})
}

func (w *oleWriter) writeTo(dst *bytes.Buffer) error {
	type layout struct {
		name      string
		data      []byte
		isMini    bool
		startSect uint32
	}
	layouts := make([]layout, len(w.streams))
	var miniStream []byte
	var miniFAT []uint32
	nextMini := uint32(0)
	for i, s := range w.streams {
		layouts[i].name = s.name
		layouts[i].data = s.data
		if uint32(len(s.data)) < oleCutoff {
			layouts[i].isMini = true
			if len(s.data) == 0 {
				layouts[i].startSect = oleEnd
				continue
			}
			layouts[i].startSect = nextMini
			n := (len(s.data) + oleMini - 1) / oleMini
			for j := 0; j < n; j++ {
				var sector [oleMini]byte
				copy(sector[:], s.data[j*oleMini:])
				miniStream = append(miniStream, sector[:]...)
				if j < n-1 {
					miniFAT = append(miniFAT, nextMini+1)
				} else {
					miniFAT = append(miniFAT, oleEnd)
				}
				nextMini++
			}
		}
	}

	numDirEntries := 1 + len(w.streams)
	numDirSectors := (numDirEntries + 3) / 4
	numMiniFAT := 0
	if len(miniFAT) > 0 {
		numMiniFAT = (len(miniFAT)*4 + oleSector - 1) / oleSector
	}
	miniContainer := (len(miniStream) + oleSector - 1) / oleSector
	regular := 0
	for _, l := range layouts {
		if !l.isMini {
			regular += (len(l.data) + oleSector - 1) / oleSector
		}
	}
	payload := numDirSectors + numMiniFAT + miniContainer + regular
	numFAT := 0
	for {
		needed := (payload + numFAT + oleSector/4 - 1) / (oleSector / 4)
		if needed == numFAT {
			break
		}
		numFAT = needed
	}

	nextSect := uint32(numFAT)
	firstDir := nextSect
	nextSect += uint32(numDirSectors)
	firstMiniFAT := oleEnd
	if numMiniFAT > 0 {
		firstMiniFAT = nextSect
		nextSect += uint32(numMiniFAT)
	}
	firstMiniCont := oleEnd
	if miniContainer > 0 {
		firstMiniCont = nextSect
		nextSect += uint32(miniContainer)
	}
	for i := range layouts {
		if !layouts[i].isMini {
			layouts[i].startSect = nextSect
			nextSect += uint32((len(layouts[i].data) + oleSector - 1) / oleSector)
		}
	}
	totalSectors := int(nextSect)

	fat := make([]uint32, numFAT*(oleSector/4))
	for i := range fat {
		fat[i] = oleFree
	}
	for i := 0; i < numFAT; i++ {
		fat[i] = oleFAT
	}
	chain := func(start uint32, n int) {
		for i := 0; i < n; i++ {
			sid := start + uint32(i)
			if i < n-1 {
				fat[sid] = sid + 1
			} else {
				fat[sid] = oleEnd
			}
		}
	}
	chain(firstDir, numDirSectors)
	if numMiniFAT > 0 {
		chain(firstMiniFAT, numMiniFAT)
	}
	if miniContainer > 0 {
		chain(firstMiniCont, miniContainer)
	}
	for _, l := range layouts {
		if l.isMini {
			continue
		}
		chain(l.startSect, (len(l.data)+oleSector-1)/oleSector)
	}
	if totalSectors > len(fat) {
		return fmt.Errorf("ole: sectors %d > FAT %d", totalSectors, len(fat))
	}
	if numFAT > 109 {
		return fmt.Errorf("ole: слишком много FAT-секторов")
	}

	var hdr [oleHeader]byte
	copy(hdr[0:8], oleMagic[:])
	binary.LittleEndian.PutUint16(hdr[24:], 0x003E)
	binary.LittleEndian.PutUint16(hdr[26:], 0x0003)
	binary.LittleEndian.PutUint16(hdr[28:], 0xFFFE)
	binary.LittleEndian.PutUint16(hdr[30:], 9)
	binary.LittleEndian.PutUint16(hdr[32:], 6)
	binary.LittleEndian.PutUint32(hdr[44:], uint32(numFAT))
	binary.LittleEndian.PutUint32(hdr[48:], firstDir)
	binary.LittleEndian.PutUint32(hdr[56:], oleCutoff)
	binary.LittleEndian.PutUint32(hdr[60:], firstMiniFAT)
	binary.LittleEndian.PutUint32(hdr[64:], uint32(numMiniFAT))
	binary.LittleEndian.PutUint32(hdr[68:], oleEnd)
	for i := 0; i < 109; i++ {
		v := oleFree
		if i < numFAT {
			v = uint32(i)
		}
		binary.LittleEndian.PutUint32(hdr[76+i*4:], v)
	}
	if _, err := dst.Write(hdr[:]); err != nil {
		return err
	}

	perFAT := oleSector / 4
	for i := 0; i < numFAT; i++ {
		var sector [oleSector]byte
		for j := 0; j < perFAT; j++ {
			idx := i*perFAT + j
			v := oleFree
			if idx < len(fat) {
				v = fat[idx]
			}
			binary.LittleEndian.PutUint32(sector[j*4:], v)
		}
		if _, err := dst.Write(sector[:]); err != nil {
			return err
		}
	}

	slots := make([][oleDirEntry]byte, numDirSectors*4)
	for i := range slots {
		binary.LittleEndian.PutUint32(slots[i][68:], oleFree)
		binary.LittleEndian.PutUint32(slots[i][72:], oleFree)
		binary.LittleEndian.PutUint32(slots[i][76:], oleFree)
		binary.LittleEndian.PutUint32(slots[i][116:], oleEnd)
	}
	rootSize := uint32(0)
	if miniContainer > 0 {
		rootSize = uint32(len(miniStream))
	}
	child := uint32(oleFree)
	if len(layouts) > 0 {
		child = 1
	}
	slots[0] = oleDir("Root Entry", oleObjRoot, oleFree, oleFree, child, firstMiniCont, rootSize)
	for i, l := range layouts {
		right := oleFree
		if i+1 < len(layouts) {
			right = uint32(i + 2)
		}
		slots[i+1] = oleDir(l.name, oleObjStream, oleFree, right, oleFree, l.startSect, uint32(len(l.data)))
	}
	for s := 0; s < numDirSectors; s++ {
		var sector [oleSector]byte
		for e := 0; e < 4; e++ {
			copy(sector[e*oleDirEntry:], slots[s*4+e][:])
		}
		if _, err := dst.Write(sector[:]); err != nil {
			return err
		}
	}

	if numMiniFAT > 0 {
		buf := make([]byte, numMiniFAT*oleSector)
		for i := range buf {
			buf[i] = 0xFF
		}
		for i, v := range miniFAT {
			binary.LittleEndian.PutUint32(buf[i*4:], v)
		}
		if _, err := dst.Write(buf); err != nil {
			return err
		}
	}
	if miniContainer > 0 {
		padded := make([]byte, miniContainer*oleSector)
		copy(padded, miniStream)
		if _, err := dst.Write(padded); err != nil {
			return err
		}
	}
	for _, l := range layouts {
		if l.isMini {
			continue
		}
		n := (len(l.data) + oleSector - 1) / oleSector
		padded := make([]byte, n*oleSector)
		copy(padded, l.data)
		if _, err := dst.Write(padded); err != nil {
			return err
		}
	}
	return nil
}

func oleDir(name string, objType byte, left, right, child, start, size uint32) [oleDirEntry]byte {
	var buf [oleDirEntry]byte
	runes := []rune(name)
	if len(runes) > 31 {
		runes = runes[:31]
	}
	words := utf16.Encode(runes)
	for i, w := range words {
		binary.LittleEndian.PutUint16(buf[i*2:], w)
	}
	binary.LittleEndian.PutUint16(buf[64:], uint16((len(words)+1)*2))
	buf[66] = objType
	buf[67] = 1
	binary.LittleEndian.PutUint32(buf[68:], left)
	binary.LittleEndian.PutUint32(buf[72:], right)
	binary.LittleEndian.PutUint32(buf[76:], child)
	binary.LittleEndian.PutUint32(buf[116:], start)
	binary.LittleEndian.PutUint32(buf[120:], size)
	return buf
}
