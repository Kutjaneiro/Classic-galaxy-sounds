//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"unsafe"
)

var getConsoleProcessList = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleProcessList")

// launchedWithoutTerminal is true when started by double-click or
// drag-and-drop: the console window then belongs to this program alone and
// would close the moment it exits, before the result can be read.
func launchedWithoutTerminal() bool {
	var pids [4]uint32
	n, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return n == 1
}

func openFolder(path string) { _ = exec.Command("explorer", path).Start() }
