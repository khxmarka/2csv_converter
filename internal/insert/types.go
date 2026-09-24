// Package insert — потоковый разбор INSERT ... VALUES из io.Reader (§4 политики).
package insert

import (
	"errors"
	"io"
)

// Meta — заголовок одного оператора INSERT.
type Meta struct {
	Table   string
	Columns []string
	Offset  int64
	Line    int
	// ValuesLine — строка файла, с которой начинается хвост после VALUES.
	// ParseValues по ней считает абсолютные номера строк (Handler.Cut).
	ValuesLine int
}

// Kind — тип ячейки VALUES.
type Kind int

const (
	Text Kind = iota
	Null
	Missing
)

// Cell — одно значение строки VALUES. Текст без окружающих SQL-кавычек.
type Cell struct {
	Kind Kind
	Text string
}

// Skip — отвергнутый INSERT. Если перед этим был Begin без End,
// получатель должен отбросить уже принятые строки.
type Skip struct {
	Offset int64
	Line   int
	Table  string
	Reason string
}

// ErrRowSurplus — строка VALUES шире допустимой; INSERT не отменяется.
var ErrRowSurplus = errors.New("значений больше, чем колонок")

// CellWriter принимает ячейки одной строки VALUES без хранения всего текста в слайсе.
type CellWriter interface {
	Null() error
	Missing() error
	// Text копирует декодированный SQL-текст значения в w (без SQL-кавычек).
	Text(write func(w io.Writer) error) error
}

// Handler принимает события разбора. Нил-колбэки пропускаются.
// Ошибка из Begin/Row/End останавливает разбор (это I/O потребителя, не SQL).
type Handler struct {
	// BeforeValues вызывается после списка колонок и до разбора ячеек VALUES.
	// skip=true — хвост statement пропускается без Cell.
	BeforeValues func(Meta) (skip bool, err error)
	// ValuesAt получает границы хвоста statement (после VALUES, до ';'
	// включительно) как смещение и длину от начала потока. Сканер ячейки не
	// разбирает и хвост не копирует: вызывающий читает диапазон сам
	// (io.SectionReader по тому же файлу) и разбирает его ParseValues.
	ValuesAt func(meta Meta, off, n int64) error
	Begin    func(Meta) error
	// Row получает одну разобранную строку VALUES.
	// Вернуть ErrRowSurplus — пропустить только эту строку.
	Row func([]Cell) error
	// StreamRow, если задан, используется вместо Row: пишет строку без []Cell.
	// emit заполняет ячейки текущей tuple; потребитель пишет CSV сам.
	// Вернуть ErrRowSurplus — пропустить строку; остальные строки INSERT продолжаются.
	StreamRow func(emit func(CellWriter) error) error
	// RowSurplus вызывается один раз на каждую пропущенную из‑за ширины строку.
	RowSurplus func()
	// Cut — INSERT оборван после принятых строк (обрезанный дамп, битая
	// последняя строка): принятые строки остаются, дальше вызывается End.
	// line — строка файла, где оборвалось.
	Cut  func(reason string, line int)
	End  func() error
	Skip func(Skip)
}
