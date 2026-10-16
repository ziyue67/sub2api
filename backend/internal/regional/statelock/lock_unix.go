//go:build !windows

// Package statelock prevents multiple processes from owning one replica file.
package statelock

import (
	"golang.org/x/sys/unix"
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
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
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
