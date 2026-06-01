//go:build !unix

package pathsafe

// noFollowFlag falls back to 0 on platforms where the syscall package does
// not export O_NOFOLLOW (Windows, etc.). The SafeJoin walk still rejects
// outward symlinks; this only loses the leaf-level race protection at write
// time. forktrust releases ship for linux + darwin only, so this is a
// "code compiles on Windows" stub rather than a Windows-supported config.
const noFollowFlag = 0
