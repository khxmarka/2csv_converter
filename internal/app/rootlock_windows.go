//go:build windows

package app

import (
	"crypto/sha256"
	"fmt"
	"syscall"
	"unsafe"
)

const (
	waitObject0   = 0x00000000
	waitAbandoned = 0x00000080
	waitTimeout   = 0x00000102
)

var (
	createMutexW  = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")
	releaseMutexW = syscall.NewLazyDLL("kernel32.dll").NewProc("ReleaseMutex")
)

func acquirePlatformRootLock(key string) (func(), error) {
	name := fmt.Sprintf(`Global\2csv-%x`, sha256.Sum256([]byte(key)))
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	rawHandle, _, callErr := createMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))
	if rawHandle == 0 {
		if callErr != syscall.Errno(0) {
			return nil, callErr
		}
		return nil, syscall.EINVAL
	}
	handle := syscall.Handle(rawHandle)
	status, waitErr := syscall.WaitForSingleObject(handle, 0)
	if status != waitObject0 && status != waitAbandoned {
		_ = syscall.CloseHandle(handle)
		if status == waitTimeout {
			return nil, fmt.Errorf("mutex занят")
		}
		if waitErr != nil {
			return nil, waitErr
		}
		return nil, fmt.Errorf("WaitForSingleObject: status 0x%x", status)
	}
	return func() {
		_, _, _ = releaseMutexW.Call(uintptr(handle))
		_ = syscall.CloseHandle(handle)
	}, nil
}
