package ui

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Confirm prints prompt to w, reads a line from r, and interprets it as a
// yes/no answer. An empty answer (just Enter) resolves to defaultYes.
func Confirm(r io.Reader, w io.Writer, prompt string, defaultYes bool) (bool, error) {
	fmt.Fprint(w, prompt)

	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return defaultYes, nil
	}
}
