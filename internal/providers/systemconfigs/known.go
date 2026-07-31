package systemconfigs

// ExcludedPaths are pacman-tracked backup files that are never captured,
// even though pacman reports them as locally modified. Verified against a
// real desktop during development: files like /etc/passwd, /etc/shadow,
// and /etc/fstab show up as "modified" purely as a side effect of normal
// system operation (useradd, disk changes, DHCP), not deliberate user
// configuration -- and reproducing them on a different machine would be
// actively harmful (UID/GID conflicts, broken disk UUIDs, broken DNS), not
// just noise. /etc/shadow and /etc/gshadow are also literally credential
// files. pacman's "modified" signal only means "differs from the package's
// shipped checksum" -- it can't distinguish deliberate customization from
// this kind of routine, machine-specific side effect, so exclusion by path
// is the primary defense here, not a confidence level.
var ExcludedPaths = map[string]bool{
	"/etc/passwd":      true,
	"/etc/shadow":      true,
	"/etc/group":       true,
	"/etc/gshadow":     true,
	"/etc/subuid":      true,
	"/etc/subgid":      true,
	"/etc/fstab":       true,
	"/etc/crypttab":    true,
	"/etc/mtab":        true,
	"/etc/hostname":    true,
	"/etc/machine-id":  true,
	"/etc/resolv.conf": true,
	"/etc/adjtime":     true,
	"/etc/localtime":   true,
}
