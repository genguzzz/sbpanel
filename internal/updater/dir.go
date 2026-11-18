package updater

import (
	"fmt"
	"os"
	"syscall"
)

// privateDir makes sure dir is a real directory owned by this user that nobody else can
// write. The helper runs as root and executes new.bin and helper.bin from it: if another
// local user could write here, they could swap a binary between its checks and the install,
// or plant symlinks for the copies to follow. With fix, a missing directory is created and
// loose permissions on our own directory are tightened (Start); without it the directory
// must already be private (the root helper refuses to work from anything else).
func privateDir(dir string, fix bool) error {
	if fix {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("upgrade directory %s is not a directory", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("upgrade directory %s is owned by uid %d, not %d", dir, st.Uid, os.Geteuid())
	}
	if fi.Mode().Perm()&0o077 == 0 {
		return nil
	}
	if !fix {
		return fmt.Errorf("upgrade directory %s is accessible to other users (mode %04o)", dir, fi.Mode().Perm())
	}
	return os.Chmod(dir, 0o700)
}
