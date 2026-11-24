package lifecycle

import (
	"io/fs"
	"path/filepath"
)

// OwnBytes is the disk a worktree would actually free: allocated blocks of
// every entry under path with a single link. Hardlinked files (node_modules
// linked from bun's cache) are shared with something else and not counted.
// Symlinks are not followed.
func OwnBytes(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if st, ok := statOf(info); ok && (st.nlink == 1 || d.IsDir()) {
			total += st.blocks * 512
		}
		return nil
	})
	return total, err
}

// SharedAwareBytes is the combined allocated size of paths with every inode
// counted once across all of them, like `du -sc a b c`.
func SharedAwareBytes(paths []string) (int64, error) {
	type key struct{ dev, ino uint64 }
	seen := map[key]bool{}
	var total int64
	for _, p := range paths {
		err := filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			st, ok := statOf(info)
			if !ok {
				return nil
			}
			k := key{st.dev, st.ino}
			if seen[k] {
				return nil
			}
			seen[k] = true
			total += st.blocks * 512
			return nil
		})
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// fileStat is the part of a stat(2) result sizing needs.
type fileStat struct {
	dev, ino, nlink uint64
	blocks          int64 // 512-byte blocks allocated
}
