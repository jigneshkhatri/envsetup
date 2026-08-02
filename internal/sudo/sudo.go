// Package sudo helps providers run a privileged command without assuming
// sudo is even available -- some machines (minimal containers especially)
// are logged into directly as root, where sudo is unnecessary and often
// not installed at all.
package sudo

import "os"

// geteuid is a var, not a direct os.Geteuid call, so tests can override it
// without depending on the real process's UID.
var geteuid = os.Geteuid

// Wrap returns the command and arguments to run name with root
// privileges: name and args unchanged if the current process is already
// root, otherwise the same command prefixed with "sudo".
func Wrap(name string, args ...string) (string, []string) {
	if geteuid() == 0 {
		return name, args
	}
	return "sudo", append([]string{name}, args...)
}
