// Disk space for vaults.

package store

import (
	"strconv"
	"strings"
	"syscall"
)

// Free returns total and free bytes on the filesystem that holds path.
// path must exist.
func Free(path string) (total, free int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bs := int64(st.Bsize)
	return int64(st.Blocks) * bs, int64(st.Bavail) * bs, nil
}

// DfSpaces reads total and free bytes from one `df -Pk <path>` output.
// Column 2 holds the 1024-blocks, column 4 holds the free blocks.
func DfSpaces(out string) (total, free int64, ok bool) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return 0, 0, false
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return 0, 0, false
	}
	blocks, err1 := strconv.ParseInt(fields[1], 10, 64)
	avail, err2 := strconv.ParseInt(fields[3], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return blocks * 1024, avail * 1024, true
}
