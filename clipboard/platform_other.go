//go:build !darwin && !linux

package clipboard

// detect has no reader on this platform yet; a Windows or WSL reader can be
// added here without touching the seam.
func detect() Reader { return unavailable{} }
