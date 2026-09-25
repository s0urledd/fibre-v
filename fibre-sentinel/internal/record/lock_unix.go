//go:build unix

package record

import (
	"errors"
	"os"
	"syscall"
)

// Rotation is supported where flock is: the writers' shared lock and the
// archiver's exclusive one are what make the swap safe.
const rotationSupported = true

func lockShared(f *os.File) error    { return flock(f, syscall.LOCK_SH) }
func lockExclusive(f *os.File) error { return flock(f, syscall.LOCK_EX) }
func unlock(f *os.File) error        { return flock(f, syscall.LOCK_UN) }

func flock(f *os.File, how int) error {
	for {
		err := syscall.Flock(int(f.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func isUnsupportedSync(err error) bool { return errors.Is(err, syscall.EINVAL) }

// ownLike gives path the owner and group of like. A run as the service
// user changes nothing; a run as root must not leave the live file, or the
// archive, to root, or the writers could no longer append to it.
func ownLike(path string, like os.FileInfo) error {
	st, ok := like.Sys().(*syscall.Stat_t)
	if !ok || os.Geteuid() != 0 {
		return nil
	}
	return os.Lchown(path, int(st.Uid), int(st.Gid))
}
