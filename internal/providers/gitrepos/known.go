package gitrepos

// KnownContainers are well-known parent directories, relative to $HOME,
// whose immediate subdirectories Discover checks for a .git repo. This is
// deliberately a shallow (one level deep), curated, and small scan -- not a
// recursive walk of arbitrary project directories, which would sweep up far
// more than "workstation setup" (plugin managers, tool checkouts) intends.
var KnownContainers = []string{
	".local/share",
	".tmux/plugins",
}
