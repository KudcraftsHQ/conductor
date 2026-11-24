//go:build unix

package lifecycle

import (
	"io/fs"
	"syscall"
)

func statOf(info fs.FileInfo) (fileStat, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileStat{}, false
	}
	return fileStat{dev: uint64(st.Dev), ino: uint64(st.Ino), nlink: uint64(st.Nlink), blocks: int64(st.Blocks)}, true
}
