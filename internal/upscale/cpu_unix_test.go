//go:build retail && unix

package upscale

import (
	"syscall"
	"time"
)

// processCPU is this process's user and system CPU time so far.
func processCPU() time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}
