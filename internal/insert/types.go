// Package insert — потоковый разбор INSERT ... VALUES из io.Reader (§4 политики).
package insert

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

// Handler принимает события разбора. Нил-колбэки пропускаются.
// Ошибка из Begin/Row/End останавливает разбор (это I/O потребителя, не SQL).
type Handler struct {
	Begin func(Meta) error
	Row   func([]Cell) error
	End   func() error
	Skip  func(Skip)
}
