package app

// poolSize — N воркеров на весь запуск: min(GOMAXPROCS, 16), не меньше 1.
// Числом файлов не ограничивается: INSERT одного файла тоже идут в этот пул.
func poolSize() int {
	return workerCount(maxWorkers)
}
