// Package insert parses INSERT ... VALUES statements from an io.Reader without materializing the dump.
package insert

import (
	"errors"
	"io"
)

// Meta describes one INSERT statement before its VALUES body.
type Meta struct {
	Table   string
	Columns []string
	Offset  int64
	Line    int
	// ValuesLine is the source line where the VALUES body starts. ParseValues
	// uses it to report absolute lines to Handler.Cut.
	ValuesLine int
	// ValuesOffset is the source byte where the VALUES body starts. ParseValues
	// uses it to report absolute error offsets.
	ValuesOffset int64
}

// Kind identifies a VALUES cell representation.
type Kind int

const (
	Text Kind = iota
	Null
	Missing
)

// Cell is one VALUES entry with surrounding SQL quotes removed from Text.
type Cell struct {
	Kind Kind
	Text string
}

// Skip describes a rejected INSERT. If Begin was emitted without End, the
// receiver must discard rows already accepted for that statement.
type Skip struct {
	Offset int64
	Line   int
	Table  string
	Reason string
}

// ErrRowSurplus reports a VALUES row wider than its accepted schema without rejecting the whole INSERT.
var ErrRowSurplus = errors.New("значений больше, чем колонок")

// CellWriter receives one VALUES row without materializing all cell text in memory.
type CellWriter interface {
	Null() error
	Missing() error
	// Text writes the decoded value without surrounding SQL quotes.
	Text(write func(w io.Writer) error) error
}

// Handler receives parser events. Nil callbacks are ignored. Callback errors
// stop parsing because they represent consumer I/O failures rather than SQL syntax.
type Handler struct {
	// BeforeValues runs after the column list and before VALUES cells are parsed.
	// Returning skip avoids parsing or emitting the statement body.
	BeforeValues func(Meta) (skip bool, err error)
	// ValuesAt receives the VALUES body range through the terminating semicolon.
	// The scanner neither parses nor copies it; callers may read the range with
	// io.SectionReader and pass it to ParseValues.
	ValuesAt func(meta Meta, off, n int64) error
	Begin    func(Meta) error
	// Row receives one parsed VALUES row. ErrRowSurplus skips only that row.
	Row func([]Cell) error
	// StreamRow replaces Row when set. emit streams cells from the current tuple
	// so the consumer can write CSV without allocating []Cell. ErrRowSurplus
	// skips only the current row.
	StreamRow func(emit func(CellWriter) error) error
	// RowSurplus runs once for each row skipped because of its width.
	RowSurplus func()
	// Cut reports an INSERT truncated after complete rows. Accepted rows remain,
	// End still follows, and line identifies the source location of the cut.
	Cut  func(reason string, line int)
	End  func() error
	Skip func(Skip)
}
