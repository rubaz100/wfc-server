//go:build !linux

package cli

// maybeElevate is a no-op outside Linux: Windows elevates through the embedded
// application manifest (UAC) and macOS binds privileged ports through the
// osascript helper in internal/privbind, so neither needs a re-exec here.
func maybeElevate([]string) (int, bool) { return 0, false }
