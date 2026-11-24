//go:build !unix

package lifecycle

import "io/fs"

// statOf has no inode or block data off Unix; sizes read as zero there.
func statOf(fs.FileInfo) (fileStat, bool) { return fileStat{}, false }
