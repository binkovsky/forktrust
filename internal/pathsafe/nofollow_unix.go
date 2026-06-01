//go:build unix

package pathsafe

import "syscall"

// noFollowFlag is the OS flag that makes open() refuse to traverse a leaf
// symlink. On Unix-family systems (linux, darwin, *bsd) syscall.O_NOFOLLOW
// is the canonical value.
const noFollowFlag = syscall.O_NOFOLLOW
