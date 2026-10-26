package xfer

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSafeName(t *testing.T) {
	long := strings.Repeat("长", 120) + ".txt"
	cases := map[string]string{
		"report.pdf":                   "report.pdf",
		"../../etc/passwd":             "passwd",
		`..\..\Windows\system32\x.dll`: "x.dll",
		"/abs/path/file.txt":           "file.txt",
		"C:\\Users\\a\\b.txt":          "b.txt",
		"a:b*c?d\"e<f>g|h.txt":         "a_b_c_d_e_f_g_h.txt",
		"tab\there\nline.txt":          "tab_here_line.txt",
		"nul\x00byte.txt":              "nul_byte.txt",
		"trailing dot.":                "trailing dot",
		"  spaces  ":                   "spaces",
		"":                             "file",
		".":                            "file",
		"..":                           "file",
		"...":                          "file",
		"CON":                          "_CON",
		"con.txt":                      "_con.txt",
		"LPT1.log":                     "_LPT1.log",
		"console.txt":                  "console.txt",
		".hidden":                      ".hidden",
		"archive.tar.gz":               "archive.tar.gz",
		"photo\u202Egpj.exe":           "photo_gpj.exe", // right-to-left override: disguises the real extension
		"bad\xffutf8.txt":              "bad_utf8.txt",
		"日本語.txt":                      "日本語.txt",
	}
	for in, want := range cases {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
	got := SafeName(long)
	if len(got) > maxNameBytes || !strings.HasSuffix(got, ".txt") || !strings.Contains(got, "长") {
		t.Errorf("long name = %q (%d bytes)", got, len(got))
	}
	if !strings.HasSuffix(SafeName(strings.Repeat("a", 500)+".png"), ".png") {
		t.Error("shortening must keep the extension")
	}
	// nothing SafeName returns can escape a directory
	for in := range cases {
		out := SafeName(in)
		if out == "" || out != filepath.Base(out) || strings.ContainsAny(out, `/\`) {
			t.Errorf("SafeName(%q) = %q is not a bare file name", in, out)
		}
	}
}

func TestUniquePathNeverOverwritesAndNeverRaces(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := UniquePath(dir, "a.txt")
	if err != nil || filepath.Base(p) != "a (1).txt" {
		t.Fatalf("UniquePath = %q, %v", p, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(data) != "mine" {
		t.Fatal("an existing file was touched")
	}
	p2, _ := UniquePath(dir, "a.txt")
	if filepath.Base(p2) != "a (2).txt" {
		t.Fatalf("second = %q", p2)
	}
	if p3, _ := UniquePath(dir, "noext"); filepath.Base(p3) != "noext" {
		t.Fatalf("no extension = %q", p3)
	}
	if p4, _ := UniquePath(dir, "noext"); filepath.Base(p4) != "noext (1)" {
		t.Fatalf("no extension, second = %q", p4)
	}

	var wg sync.WaitGroup
	seen := sync.Map{}
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := UniquePath(dir, "race.bin")
			if err != nil {
				t.Error(err)
				return
			}
			if _, dup := seen.LoadOrStore(p, true); dup {
				t.Errorf("%s handed out twice", p)
			}
		}()
	}
	wg.Wait()
	if _, err := UniquePath(filepath.Join(dir, "missing", "dir"), "x"); err == nil {
		t.Fatal("a missing directory must be an error, not an endless loop")
	}
}

func TestFreeSpaceReportsSomethingSensible(t *testing.T) {
	n, err := freeSpace(t.TempDir())
	if err != nil || n == 0 {
		t.Fatalf("freeSpace = %d, %v", n, err)
	}
	if _, err := freeSpace(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing directory must be an error")
	}
}
