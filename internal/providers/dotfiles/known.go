package dotfiles

// KnownPaths are well-known, high-confidence config file locations,
// relative to $HOME, that Discover checks for. This is a deliberately
// curated allowlist -- EnvSetup never walks the filesystem guessing at
// arbitrary files; a path only becomes a resource if it's on this list AND
// actually exists.
var KnownPaths = []string{
	".zshrc",
	".zprofile",
	".bashrc",
	".bash_profile",
	".profile",
	".gitconfig",
	".vimrc",
	".tmux.conf",
	".config/nvim/init.lua",
	".config/nvim/init.vim",
	".config/git/config",
	".config/git/ignore",
	".config/starship.toml",
	".config/kitty/kitty.conf",
	".config/alacritty/alacritty.toml",
	".config/alacritty/alacritty.yml",
	".config/fish/config.fish",
}
