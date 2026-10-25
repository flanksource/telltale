//go:build !darwin

package groups

import "context"

// readJobs reads none: there is no launchd.
func readJobs(context.Context) (Jobs, error) { return Jobs{}, nil }
