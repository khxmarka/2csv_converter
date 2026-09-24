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
	// Values забирает сырой хвост statement в память.
	// Для больших INSERT задайте ValuesFile: хвост пишется во временный файл.
	Values func(Meta, []byte) error
	// ValuesFile получает путь к файлу с хвостом statement. Файл уже закрыт.
	// Вызывающий удаляет его. Если задан, Values игнорируется.
	ValuesFile func(Meta, string) error
	// SpillDir — каталог временного файла для ValuesFile. Пусто — TempDir.
	SpillDir string
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
	End        func() error
	Skip       func(Skip)
}
