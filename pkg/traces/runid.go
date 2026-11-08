package traces

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

const RunTimestampLayout = "20060102T150405Z"

const ManifestFile = "run.json"

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,9}[0-9]{8}T[0-9]{6}Z(?:_[2-9][0-9]*|_1[0-9]+)?$`)

// IsRunID identifies compact generated IDs, including collision suffixes.
func IsRunID(id string) bool { return runIDPattern.MatchString(id) }

// RunID names a run by its short hostname and UTC start time.
func RunID(started time.Time) string {
	hostname, err := os.Hostname()
	if err != nil {
		panic(fmt.Errorf("name trace run: hostname: %w", err))
	}
	prefix, err := hostnamePrefix(hostname)
	if err != nil {
		panic(err)
	}
	return prefix + started.UTC().Format(RunTimestampLayout)
}

func hostnamePrefix(hostname string) (string, error) {
	prefix := strings.ReplaceAll(strings.Split(hostname, ".")[0], "-", "")
	if prefix == "" {
		return "", fmt.Errorf("name trace run: hostname %q has no short name", hostname)
	}
	for _, char := range prefix {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return "", fmt.Errorf("name trace run: hostname %q contains an invalid character", hostname)
		}
	}
	return prefix[:min(9, len(prefix))], nil
}
