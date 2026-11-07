package load

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// cpuSeconds returns this process's cumulative user+system CPU time in seconds,
// read from /proc/self/stat. Linux CLK_TCK is 100 ticks per second. The comm
// field (field 2) can contain spaces, so the record is parsed from the last ')'.
func cpuSeconds() (float64, error) {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	idx := strings.LastIndex(s, ")")
	if idx < 0 || idx == len(s)-1 {
		return 0, errors.New("malformed /proc/self/stat")
	}
	fields := strings.Fields(s[idx+1:])
	// fields[0] is field 3 (state); utime is field 14, stime field 15.
	if len(fields) < 13 {
		return 0, errors.New("short /proc/self/stat")
	}
	utime, err := strconv.ParseInt(fields[11], 10, 64)
	if err != nil {
		return 0, err
	}
	stime, err := strconv.ParseInt(fields[12], 10, 64)
	if err != nil {
		return 0, err
	}
	return float64(utime+stime) / 100.0, nil
}
