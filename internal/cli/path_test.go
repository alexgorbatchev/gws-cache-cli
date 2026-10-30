package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCachePathDisplayAndExpansion(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(base)
	for _, mode := range []string{"0", "1"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", mode)
			for _, tc := range []struct{ name, input, display, disk string }{
				{"absolute", filepath.Join(home, "cache.db"), "~/cache.db", filepath.Join(home, "cache.db")},
				{"tilde", "~/nested/cache.db", "~/nested/cache.db", filepath.Join(home, "nested/cache.db")},
				{"dot-prefix", filepath.Join(home, "..child/cache.db"), "~/..child/cache.db", filepath.Join(home, "..child/cache.db")},
				{"sibling", filepath.Join(base, "home-other/cache.db"), filepath.Join(base, "home-other/cache.db"), filepath.Join(base, "home-other/cache.db")},
				{"relative", "relative/cache.db", "relative/cache.db", filepath.Join(base, "relative/cache.db")},
			} {
				t.Run(tc.name, func(t *testing.T) {
					out, err := runShared(t, tc.input, "cache", "status")
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(out, tc.display) {
						t.Fatalf("output %q lacks %q", out, tc.display)
					}
					if _, err := os.Stat(tc.disk); err != nil {
						t.Fatalf("database not created at expected path: %v", err)
					}
				})
			}
		})
	}
}

func TestDefaultCachePathHelp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	for _, mode := range []string{"0", "1"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", mode)
			cmd := NewRootCmd("test")
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"--help"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), home) || !strings.Contains(out.String(), "~/cache/gws-cache/cache.db") {
				t.Fatalf("unfolded help: %s", &out)
			}
		})
	}
}

func TestPathHelpers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, tc := range []struct{ input, display, expanded string }{
		{home, "~", home},
		{"~", "~", home},
		{"~other/file", "~other/file", "~other/file"},
		{"", "", ""},
		{filepath.Dir(home), filepath.Dir(home), filepath.Dir(home)},
	} {
		t.Run(tc.input, func(t *testing.T) {
			if got := displayPath(tc.input); got != tc.display {
				t.Fatalf("display = %q, want %q", got, tc.display)
			}
			got, err := expandPath(tc.input)
			if err != nil || got != tc.expanded {
				t.Fatalf("expand = %q, %v; want %q", got, err, tc.expanded)
			}
		})
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if got := displayPath(home); got != home {
		t.Fatalf("missing home changed display to %q", got)
	}
	if _, err := expandPath("~/cache.db"); err == nil {
		t.Fatal("missing home must reject expansion")
	}
	if _, err := (&app{dbPath: "~/cache.db"}).openStore(); err == nil {
		t.Fatal("missing home must reject opening")
	}
}
