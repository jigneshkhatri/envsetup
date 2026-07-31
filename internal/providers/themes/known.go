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

// maxThemeWalkDepth bounds how deep Discover walks into a single theme
// directory -- generous enough to never be a practical limit for a real
// theme, just a guard against a pathological tree.
const maxThemeWalkDepth = 12

// excludedThemeDirNames are directory names skipped anywhere while walking
// a theme's tree.
var excludedThemeDirNames = map[string]bool{
	".git": true, "Cache": true, "__pycache__": true,
}
