package vault

import (
	"testing"

	g "github.com/tobischo/gokeepasslib/v3"
)

// Entries written by ClipBridge (the app's former name) carry ClipBridge.* CustomData keys.
func TestClipBridgeKeysAreReadAndReplacedOnWrite(t *testing.T) {
	le := g.NewEntry()
	le.CustomData = []g.CustomData{
		{Key: "ClipBridge.Kind", Value: "apikey"},
		{Key: "ClipBridge.SSH.Hosts", Value: "github.com"},
		{Key: "Other.Plugin", Value: "kept"},
	}
	if got := getCustom(&le, cdKind); got != "apikey" {
		t.Fatalf("legacy kind = %q", got)
	}
	if got := getCustom(&le, cdSSHHosts); got != "github.com" {
		t.Fatalf("legacy hosts = %q", got)
	}
	setCustom(&le, cdKind, "login")
	if getCustom(&le, cdKind) != "login" {
		t.Fatal("new value not read back")
	}
	for _, cd := range le.CustomData {
		if cd.Key == "ClipBridge.Kind" {
			t.Fatal("the legacy key survived a write")
		}
	}
	delCustom(&le, cdSSHHosts)
	if getCustom(&le, cdSSHHosts) != "" {
		t.Fatal("legacy hosts survived a delete")
	}
	if getCustom(&le, "Other.Plugin") != "kept" {
		t.Fatal("an unrelated key was touched")
	}
	if !isCustomKey("ClipBridge.PrevGroup", cdPrevGroup) || !isCustomKey(cdPrevGroup, cdPrevGroup) || isCustomKey("", cdPrevGroup) {
		t.Fatal("isCustomKey")
	}
	if legacyKey("Other.Plugin") != "" {
		t.Fatal("legacyKey of a foreign key")
	}
}
