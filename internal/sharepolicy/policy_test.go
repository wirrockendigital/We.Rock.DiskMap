package sharepolicy

import (
	"strings"
	"testing"
)

// TestVisibilityPolicy rejects ambiguous flags and refuses roots outside a qualified share.
func TestVisibilityPolicy(t *testing.T) {
	text := "[example]\npath=/volume1/example\nedit synoacl=yes\nskip smb perm=yes\nhide unreadable=yes\nftp disable list=no\n"
	rights, err := Parse(strings.NewReader(text))
	if err != nil || !rights["example"].HideUnreadable {
		t.Fatal(rights, err)
	}
	snapshot := Snapshot{Rights: rights}
	for _, root := range []string{"/volume1/example", "/volume1/example/child"} {
		if _, err := snapshot.ForRoot(root); err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range []string{"/volume1", "/volume1/example-other", "/volume1/example/../other"} {
		if _, err := snapshot.ForRoot(root); err == nil {
			t.Fatal("unqualified root accepted", root)
		}
	}
	for _, field := range []string{"hide unreadable", "ftp disable list", "edit synoacl", "skip smb perm"} {
		if _, err := Parse(strings.NewReader("[example]\npath=/volume1/example\n" + field + "=perhaps\n")); err == nil {
			t.Fatal("ambiguous policy accepted", field)
		}
	}
	for _, field := range []string{"edit synoacl", "skip smb perm"} {
		denied, _ := Parse(strings.NewReader(strings.ReplaceAll(text, field+"=yes", field+"=no")))
		if _, err := (Snapshot{Rights: denied}).ForRoot("/volume1/example"); err == nil {
			t.Fatal("unqualified rights mode accepted")
		}
	}
	denied, _ := Parse(strings.NewReader(strings.ReplaceAll(text, "ftp disable list=no", "ftp disable list=yes")))
	if _, err := (Snapshot{Rights: denied}).ForRoot("/volume1/example"); err == nil {
		t.Fatal("disabled listing accepted")
	}
}
