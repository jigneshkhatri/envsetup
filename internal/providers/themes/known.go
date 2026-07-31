package themes

// KnownContainers are well-known theme directories, relative to $HOME,
// whose immediate subdirectories Discover treats as candidate themes --
// the same shallow, one-level-deep scan discipline as git_repos.
var KnownContainers = []string{
	".local/share/themes", // GTK themes
	".local/share/icons",  // icon themes
	".icons",              // legacy icon/cursor theme location
	".themes",             // legacy GTK theme location
}

// SystemContainers are well-known theme directories at system-wide
// (absolute, root-owned) paths, whose immediate subdirectories Discover
// treats as candidate themes -- same shallow, one-level-deep scan
// discipline as the user-space KnownContainers. Unlike user-space themes,
// a system container also holds themes installed by packages (e.g.
// plasma-desktop ships its own SDDM themes), so Discover filters those out
// via pacman ownership: only paths pacman doesn't know about are
// candidates, since package-owned ones are already reproducible through
// the packages provider.
var SystemContainers = []string{
	"/usr/share/themes", // GTK themes installed system-wide
	"/usr/share/icons",  // icon themes installed system-wide
	sddmThemesContainer,
}

// sddmThemesContainer is the standard location for SDDM (display manager)
// themes. It gets special handling beyond the other system containers:
// Discover also checks whether a found theme is the currently active SDDM
// theme, and Apply can (re-)activate it.
const sddmThemesContainer = "/usr/share/sddm/themes"

// sddmConfFile and sddmConfDir are where SDDM's own configuration lives;
// read (never written) to determine the currently active theme.
const (
	sddmConfFile = "/etc/sddm.conf"
	sddmConfDir  = "/etc/sddm.conf.d"
)

// sddmActivationFile is a dedicated, EnvSetup-owned config fragment used to
// select the active SDDM theme. Apply only ever writes this exact file --
// never an existing sddm.conf.d fragment that might hold unrelated
// settings someone else authored.
const sddmActivationFile = sddmConfDir + "/envsetup-theme.conf"

// maxThemeWalkDepth bounds how deep Discover walks into a single theme
// directory -- generous enough to never be a practical limit for a real
// theme, just a guard against a pathological tree.
const maxThemeWalkDepth = 12

// excludedThemeDirNames are directory names skipped anywhere while walking
// a theme's tree.
var excludedThemeDirNames = map[string]bool{
	".git": true, "Cache": true, "__pycache__": true,
}
