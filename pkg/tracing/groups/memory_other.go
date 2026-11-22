//go:build !darwin && !linux

package groups

import "context"

// Per-process swap and compressed memory are unavailable on other platforms.
func readMemory(ctx context.Context, _ []Group, _ map[string]Reading, _ bool) error {
	return ctx.Err()
}
