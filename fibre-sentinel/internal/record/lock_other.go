//go:build !unix

package record

import "os"

// Without flock there is no safe swap under a running writer: Archive
// refuses, and the locks are no-ops so the writers and readers still work
// (a file here is never rotated, so it has base 0 throughout).
const rotationSupported = false

func lockShared(*os.File) error    { return nil }
func lockExclusive(*os.File) error { return nil }
func unlock(*os.File) error        { return nil }

func isUnsupportedSync(error) bool { return true }

func ownLike(string, os.FileInfo) error { return nil }
