//go:build retail && !unix

package upscale

import "time"

// processCPU is not measured on this platform, so the timing tests log a zero
// CPU cost beside their wall time.
func processCPU() time.Duration { return 0 }
