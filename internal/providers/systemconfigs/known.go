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

// KnownDropInDirs are well-known "drop-in" directories: places a package
// expects to exist but never ships files inside itself (e.g. sddm ships
// /etc/sddm.conf but not /etc/sddm.conf.d or anything in it), so any file
// found there was placed by hand and is invisible to pacman's backup-file
// tracking -- pacman only tracks files it installed, and nothing installs
// the drop-in file itself. Discover lists each directory's immediate
// regular files (one level, no recursion -- that's how every one of these
// is actually used) and keeps only the ones pacman doesn't own; a
// package-owned file here is already reproducible through the packages
// provider.
var KnownDropInDirs = []string{
	"/etc/sddm.conf.d",
	"/etc/systemd/system",
	"/etc/systemd/logind.conf.d",
	"/etc/systemd/journald.conf.d",
	"/etc/systemd/resolved.conf.d",
	"/etc/systemd/timesyncd.conf.d",
	"/etc/systemd/system.conf.d",
	"/etc/systemd/user.conf.d",
	"/etc/sysctl.d",
	"/etc/modules-load.d",
	"/etc/modprobe.d",
	"/etc/tmpfiles.d",
	"/etc/udev/rules.d",
	"/etc/X11/xorg.conf.d",
	"/etc/NetworkManager/conf.d",
	"/etc/NetworkManager/dispatcher.d",
	"/etc/pacman.d/hooks",
	"/etc/polkit-1/rules.d",
}
