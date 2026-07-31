package gitrepos

// KnownContainers are well-known parent directories, relative to $HOME,
// whose immediate subdirectories Discover checks for a .git repo. This is
// deliberately a shallow (one level deep), curated, and small scan -- not a
// recursive walk of arbitrary project directories, which would sweep up far
// more than "workstation setup" (plugin managers, tool checkouts) intends.
var KnownContainers = []string{
	".local/share",
	".tmux/plugins",
	// Nested plugin-manager layouts: rather than making the scanner
	// recursive (which would risk sweeping up arbitrary project trees),
	// each is added as its own one-level-deep container.
	".oh-my-zsh/custom/plugins",
	".oh-my-zsh/custom/themes",
	".zinit/plugins",
	".local/share/zinit/plugins",
	".vim/plugged",
	".local/share/nvim/lazy",
	".local/share/nvim/site/pack/packer/start",
	".local/share/nvim/site/pack/packer/opt",
}
