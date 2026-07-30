package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfirm(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		defaultYes bool
		want       bool
	}{
		{"yes", "y\n", false, true},
		{"no", "n\n", true, false},
		{"empty defaults to true", "\n", true, true},
		{"empty defaults to false", "\n", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			got, err := Confirm(strings.NewReader(tt.input), &out, "prompt: ", tt.defaultYes)
			if err != nil {
				t.Fatalf("Confirm: %v", err)
			}
			if got != tt.want {
				t.Errorf("Confirm() = %v, want %v", got, tt.want)
			}
			if !strings.Contains(out.String(), "prompt: ") {
				t.Errorf("prompt not printed: %q", out.String())
			}
		})
	}
}
