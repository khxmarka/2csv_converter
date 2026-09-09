package app

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var (
	activeRootsMu sync.Mutex
	activeRoots   = make(map[string]struct{})
)

func acquireRootLock(root string) (func(), error) {
	key := rootLockKey(root)
	activeRootsMu.Lock()
	if _, exists := activeRoots[key]; exists {
		activeRootsMu.Unlock()
		return nil, fmt.Errorf("корень уже обрабатывается другим экземпляром: %s", root)
	}
	activeRoots[key] = struct{}{}
	activeRootsMu.Unlock()

	releasePlatform, err := acquirePlatformRootLock(key)
	if err != nil {
		activeRootsMu.Lock()
		delete(activeRoots, key)
		activeRootsMu.Unlock()
		return nil, fmt.Errorf("корень уже обрабатывается другим экземпляром: %s: %w", root, err)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			releasePlatform()
			activeRootsMu.Lock()
			delete(activeRoots, key)
			activeRootsMu.Unlock()
		})
	}, nil
}

func rootLockKey(root string) string {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	root = filepath.Clean(root)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
	}
	return root
}
