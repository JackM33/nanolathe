//go:build unix

package main

import (
	"syscall"
	"time"
)

// processCPU is this process's user and system CPU time so far.
func processCPU() (time.Duration, bool) {
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) != nil {
		return 0, false
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano()), true
}
