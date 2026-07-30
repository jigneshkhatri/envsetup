package ui

import "os"

// colorsEnabled honors the NO_COLOR convention: https://no-color.org.
func colorsEnabled() bool {
	return os.Getenv("NO_COLOR") == ""
}

func green(s string) string  { return wrap(s, "32") }
func yellow(s string) string { return wrap(s, "33") }
func red(s string) string    { return wrap(s, "31") }

func wrap(s, code string) string {
	if !colorsEnabled() {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
