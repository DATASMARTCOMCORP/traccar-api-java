package api

import (
	"bufio"
	"os"
	"slices"
	"strings"
	"syscall"
)

// storageSpace is Log.getStorageSpace: usable and total bytes of every mounted file system
// larger than 1 GB, the fullest first, flattened into [usable, total, usable, total, ...].
func storageSpace() []int64 {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return []int64{}
	}
	defer f.Close()
	type store struct{ usable, total int64 }
	var stores []store
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		var st syscall.Statfs_t
		if syscall.Statfs(fields[1], &st) != nil {
			continue
		}
		total := int64(st.Blocks) * int64(st.Frsize)
		if total > 1_000_000_000 {
			stores = append(stores, store{int64(st.Bavail) * int64(st.Frsize), total})
		}
	}
	slices.SortStableFunc(stores, func(a, b store) int {
		ra, rb := float64(a.usable)/float64(a.total), float64(b.usable)/float64(b.total)
		switch {
		case ra < rb:
			return -1
		case ra > rb:
			return 1
		}
		return 0
	})
	out := make([]int64, 0, 2*len(stores))
	for _, s := range stores {
		out = append(out, s.usable, s.total)
	}
	return out
}
