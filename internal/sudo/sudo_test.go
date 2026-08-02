package sudo

import (
	"reflect"
	"testing"
)

func withEuid(euid int, fn func()) {
	orig := geteuid
	geteuid = func() int { return euid }
	defer func() { geteuid = orig }()
	fn()
}

func TestWrapAsRootRunsDirectly(t *testing.T) {
	withEuid(0, func() {
		name, args := Wrap("cp", "a", "b")
		if name != "cp" || !reflect.DeepEqual(args, []string{"a", "b"}) {
			t.Errorf("got %q %v, want cp [a b]", name, args)
		}
	})
}

func TestWrapAsNonRootPrefixesSudo(t *testing.T) {
	withEuid(1000, func() {
		name, args := Wrap("cp", "a", "b")
		if name != "sudo" || !reflect.DeepEqual(args, []string{"cp", "a", "b"}) {
			t.Errorf("got %q %v, want sudo [cp a b]", name, args)
		}
	})
}
