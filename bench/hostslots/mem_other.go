//go:build !linux

package hostslots

func hostMemory() int64 { return 0 }
