//go:build !linux

package monitoring

func ContainerResourceScope() bool         { return false }
func containerCPU() (int, float64, bool)   { return 0, 0, false }
func containerCPUCores() (int, bool)       { return 0, false }
func containerRam() (RamInfo, bool)        { return RamInfo{}, false }
func containerSwap() (RamInfo, bool)       { return RamInfo{}, false }
func ResourceScopeInfo() map[string]string { return map[string]string{"mode": "host"} }
