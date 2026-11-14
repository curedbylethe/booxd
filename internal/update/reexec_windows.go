//go:build windows

package update

// Restart is unsupported on Windows: Install refuses before replacing anything,
// so an upgrade runs install.ps1 instead.
func Restart(_ string, _ []string) error {
	return errManualUpdate
}
