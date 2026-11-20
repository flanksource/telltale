package groups

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

func parseCompressed(reader io.Reader) (map[int]*uint64, error) {
	values := map[int]*uint64{}
	scanner := bufio.NewScanner(reader)
	header := false
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if strings.Join(fields, " ") == "PID CMPRS" {
			header = true
			continue
		}
		if !header || len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("compressed memory: expected PID CMPRS, got %q", scanner.Text())
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid < 0 {
			return nil, fmt.Errorf("compressed memory: invalid PID %q", fields[0])
		}
		size, err := parseMemorySize(fields[1])
		if err != nil {
			return nil, err
		}
		if pid > 0 {
			values[pid] = &size
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("compressed memory listing: %w", err)
	}
	if !header || len(values) == 0 {
		return nil, fmt.Errorf("compressed memory listing has no PID CMPRS readings")
	}
	return values, nil
}

func parseMemorySize(raw string) (uint64, error) {
	if len(raw) < 2 {
		return 0, fmt.Errorf("compressed memory: invalid size %q", raw)
	}
	unit := strings.IndexByte("BKMGTPE", raw[len(raw)-1])
	value, err := strconv.ParseFloat(raw[:len(raw)-1], 64)
	value *= math.Pow(1024, float64(unit))
	if err != nil || unit < 0 || math.IsNaN(value) || value < 0 || value >= math.Exp2(64) {
		return 0, fmt.Errorf("compressed memory: invalid size %q", raw)
	}
	return uint64(value), nil
}

// MemorySize preserves unavailable readings as a dash.
func MemorySize(value *uint64) string {
	if value == nil {
		return "-"
	}
	return Size(float64(*value))
}
