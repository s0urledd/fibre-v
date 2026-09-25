//go:build unix

package record

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Run as root (by hand, or a unit without User=), a rotation leaves the new
// live file, the archive directory and its lock to the data's owner, with
// the live file's mode: the writers, running as that owner, must still be
// able to append, and the next run as that owner to take the lock.
func TestRotationKeepsOwnerAndMode(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to hand files to another owner")
	}
	dir := t.TempDir()
	const uid, gid = 65534, 65534
	os.Chown(dir, uid, gid)
	path := filepath.Join(dir, "measurements.jsonl")
	for d := 0; d < 3; d++ {
		appendLines(t, path, lineAt(t0.Add(time.Duration(d)*24*time.Hour), "a", d))
	}
	os.Chown(path, uid, gid)
	os.Chmod(path, 0o640)
	archiveAt(t, path, t0.Add(2*24*time.Hour))
	for _, p := range []string{path, filepath.Join(dir, Dir), ArchiveDir(path), filepath.Join(dir, Dir, LockFile)} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		st := info.Sys().(*syscall.Stat_t)
		if st.Uid != uid || st.Gid != gid {
			t.Fatalf("%s is owned by %d:%d, want %d:%d", p, st.Uid, st.Gid, uid, gid)
		}
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Fatalf("live file mode %v, want 0640", info.Mode().Perm())
	}
}
