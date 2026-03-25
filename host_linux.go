//go:build linux

package main

import "syscall"

type syscallStatfs = syscall.Statfs_t

func statfs(path string, buf *syscallStatfs) error {
	return syscall.Statfs(path, buf)
}
