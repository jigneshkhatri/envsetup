package fonts

// KnownDirs are the user-level font directories, relative to $HOME, that
// Discover walks recursively. System-wide font directories (/usr/share/
// fonts) are deliberately out of scope -- those are package-managed
// territory already covered by the packages provider, and tracking them
// here too would just create duplicate, conflicting state.
var KnownDirs = []string{
	".local/share/fonts",
	".fonts",
}

// extensions are the font file types Discover recognizes. Anything else
// found under KnownDirs (readmes, license files, fontconfig cache files) is
// ignored.
var extensions = map[string]bool{
	".ttf":   true,
	".otf":   true,
	".woff":  true,
	".woff2": true,
	".ttc":   true,
}
