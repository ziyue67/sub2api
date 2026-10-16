package statelock

import (
	"golang.org/x/sys/windows"
	"os"
)

type Lock struct {
	path string
	file *os.File
}

func New(path string) *Lock { return &Lock{path: path} }
func (l *Lock) TryLock() (bool, error) {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return false, err
	}
	if err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{}); err != nil {
		_ = f.Close()
		return false, err
	}
	l.file = f
	return true, nil
}
func (l *Lock) Unlock() error {
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
