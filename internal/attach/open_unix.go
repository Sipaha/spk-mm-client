//go:build unix

package attach

import "syscall"

// openFlags: a FIFO swapped in after the regular-file check still opens at
// once instead of waiting for a writer.
const openFlags = syscall.O_NONBLOCK
