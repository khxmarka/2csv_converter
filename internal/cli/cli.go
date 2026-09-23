// Package cli разбирает аргументы командной строки и вызывает app.
package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"strings"

	"sql2csv/internal/app"
	"sql2csv/internal/config"
	"sql2csv/internal/logx"
)

const (
	exitOK    = 0
	exitFatal = 1
	exitUsage = 2
)

// Run выполняет запуск и возвращает код выхода процесса.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("2csv", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { printUsage(stderr) }
	help := flags.Bool("help", false, "показать справку и выйти")
	flags.BoolVar(help, "h", false, "то же, что --help")

	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *help {
		printUsage(stdout)
		return exitOK
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "error: лишние аргументы: %s\n", strings.Join(flags.Args(), " "))
		printUsage(stderr)
		return exitUsage
	}

	if _, err := fmt.Fprint(stderr, "combo/db: "); err != nil {
		return exitFatal
	}
	root, err := readRoot(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitUsage
	}

	log := logx.New(stderr)
	if _, err := app.Run(log, root); err != nil {
		log.Errorf("%v", err)
		return exitFatal
	}
	return exitOK
}

func readRoot(stdin io.Reader) (string, error) {
	if stdin == nil {
		return "", fmt.Errorf("ожидалось combo или db")
	}
	sc := bufio.NewScanner(stdin)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("ожидалось combo или db")
	}
	return config.RootFor(sc.Text())
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `2csv — конвертер SQL INSERT, табличных .sql и Excel (.xlsx / .xls) в CSV.

После запуска спрашивает combo/db:
  db     рекурсивно обходит %s
  combo  рекурсивно обходит %s

В выбранном корне:
  .sql         INSERT ... VALUES с PII в таблице или минимум в двух колонках → CSV рядом
               табличный дамп (шапка в первой непустой строке, не SQL) → {stem}.csv
  .xlsx, .xls  каждый лист → отдельный CSV рядом с книгой
               (больше 5 листов — книга целиком пропускается)

Пул воркеров один на весь запуск.
CSV больше 1_000_000 строк данных режется на части до 500_000:
  {имя}.csv, {имя}_2.csv, {имя}_3.csv.
Так же режутся уже лежавшие .csv. converted.txt не режется.
.sql, .xlsx и .xls удаляются, если из файла получен хотя бы один CSV.

Прогресс: папка в обработке: <имя> (<N> с), обновление раз в секунду.
Несколько активных папок перечисляются вместе, у каждой свои секунды.
converted.txt — только верхние папки, для которых в этом запуске создан хотя бы один CSV.
Пустые папки и папки с нулём CSV в список не входят.
При следующих запусках папки из списка не обходятся; сброс — удалить файл вручную.
Существующий целевой CSV заменяется, варианты (n) не создаются.

Использование:
  2csv          спросить combo/db и запустить обработку
  2csv --help   показать эту справку

Коды выхода:
  0  корень существует и обработан
  1  выбранный корень отсутствует или не является директорией
  2  ошибка в аргументах командной строки или неверный ответ combo/db

Правила обработки описаны в CONSTRAINTS_AND_POLICY.md.
`, config.RootDB, config.RootCombo)
}
