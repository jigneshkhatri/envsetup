package dotfiles

// ExcludedHomeFiles are top-level $HOME dotfiles that must never be
// captured, regardless of the blanket scan -- each can carry live
// credentials or command/query history.
var ExcludedHomeFiles = map[string]bool{
	".netrc":            true,
	".pypirc":           true,
	".my.cnf":           true,
	".Xauthority":       true,
	".ICEauthority":     true,
	".bash_history":     true,
	".zsh_history":      true,
	".python_history":   true,
	".mysql_history":    true,
	".psql_history":     true,
	".rediscli_history": true,
	".lesshst":          true,
	".viminfo":          true,
}

// ExcludedConfigApps are top-level .config/<app> directories that are
// never captured, even as a low-confidence candidate -- browsers and
// chat/API clients whose "config" directory is really session state,
// cached credentials, or OAuth tokens, plus a few desktop-wide state
// stores (dconf, pulse, session) that aren't meaningfully "configuration"
// at all. This matters because `export --yes` skips interactive review
// entirely, so confidence level alone isn't a strong enough safety net for
// directories this sensitive.
//
// Verified against a real desktop during development: dconf turned out to
// be a binary database (not text config), pulse/cookie is a literal binary
// auth cookie, and session holds window-manager restore state, not
// anything a user would call "configuration".
var ExcludedConfigApps = map[string]bool{
	"google-chrome":          true,
	"google-chrome-beta":     true,
	"google-chrome-unstable": true,
	"chromium":               true,
	"BraveSoftware":          true,
	"microsoft-edge":         true,
	"vivaldi":                true,
	"opera":                  true,
	"discord":                true,
	"discordcanary":          true,
	"Slack":                  true,
	"Element":                true,
	"signal-desktop":         true,
	"Signal":                 true,
	"Postman":                true,
	"insomnia":               true,
	"thunderbird":            true,
	"dconf":                  true,
	"pulse":                  true,
	"session":                true,
	"libaccounts-glib":       true,
	"mozilla":                true,
}

// excludedConfigDirNames are directory names skipped anywhere while
// walking an app's config tree -- caches, browser/Electron local storage,
// and other generated state that isn't meaningfully "configuration". Names
// starting with "Cache" (Cache, CachedData, CachedProfilesData, ...) are
// additionally skipped by prefix, checked separately in provider.go.
var excludedConfigDirNames = map[string]bool{
	"Cache": true, "Code Cache": true, "GPUCache": true,
	"DawnCache": true, "ShaderCache": true, "GrShaderCache": true,
	"Local Storage": true, "Session Storage": true, "IndexedDB": true,
	"databases": true, "blob_storage": true, "Service Worker": true,
	"WebStorage": true, "logs": true, "Logs": true, "Crash Reports": true,
	"Crashpad": true, ".git": true, "node_modules": true, "__pycache__": true,
	"globalStorage": true, "workspaceStorage": true, "History": true,
}

// excludedConfigExtensions are file extensions skipped anywhere while
// walking an app's config tree -- binary caches/state, not text
// configuration.
var excludedConfigExtensions = map[string]bool{
	".sqlite": true, ".sqlite3": true, ".sqlite-wal": true, ".sqlite-shm": true,
	".db": true, ".db-wal": true, ".db-shm": true, ".db-journal": true,
	".ldb": true, ".log": true, ".lock": true, ".pid": true,
	".sock": true, ".tmp": true,
}

// excludedConfigFilenames are exact file names skipped anywhere while
// walking an app's config tree.
var excludedConfigFilenames = map[string]bool{
	"SingletonLock": true, "SingletonCookie": true, "SingletonSocket": true,
	".DS_Store": true,
}

// maxConfigWalkDepth bounds how deep Discover walks into a single
// .config/<app> directory, so a pathological tree can't blow up scan time.
const maxConfigWalkDepth = 4
