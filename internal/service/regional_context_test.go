package service

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Real confined files exercise valid aliases, escapes, malformed data and special files.
func TestRegionalContextQualification(t *testing.T) {
	data, err := os.ReadFile("/usr/share/zoneinfo/Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"valid", "relative", "escape", "inner-escape", "regular-localtime", "malformed", "oversize", "fifo", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			zones := filepath.Join(dir, "zones")
			if err := os.MkdirAll(filepath.Join(zones, "Europe"), 0700); err != nil {
				t.Fatal(err)
			}
			zone := filepath.Join(zones, "Europe/Amsterdam")
			if err := os.WriteFile(zone, data, 0600); err != nil {
				t.Fatal(err)
			}
			local := filepath.Join(dir, "localtime")
			target := zone
			switch kind {
			case "relative":
				target = "zones/Europe/Amsterdam"
			case "escape":
				target = filepath.Join(dir, "Europe/Amsterdam")
			case "inner-escape":
				os.Remove(zone)
				os.WriteFile(filepath.Join(dir, "outside"), data, 0600)
				os.Symlink(filepath.Join(dir, "outside"), zone)
			case "malformed":
				os.WriteFile(zone, []byte("private malformed content"), 0600)
			case "oversize":
				os.WriteFile(zone, make([]byte, (1<<20)+1), 0600)
			case "fifo":
				os.Remove(zone)
				if err := syscall.Mkfifo(zone, 0600); err != nil {
					t.Fatal(err)
				}
			case "unknown":
				target = filepath.Join(zones, "Unknown/Zone")
			}
			if kind == "regular-localtime" {
				os.WriteFile(local, data, 0600)
			} else if err := os.Symlink(target, local); err != nil {
				t.Fatal(err)
			}
			got := regionalContext(local, zones)
			valid := kind == "valid" || kind == "relative"
			if !ValidRegionalContext(got) || (got.NASTimeZone != nil) != valid {
				t.Fatalf("%+v", got)
			}
			if valid && *got.NASTimeZone != "Europe/Amsterdam" {
				t.Fatal(got)
			}
		})
	}
}

// The registered read endpoint rejects selectors and mutation methods without private diagnostics.
func TestRegionalContextRoute(t *testing.T) {
	mux := http.NewServeMux()
	registerRegionalContext(mux)
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"GET", "/api/v1/regional-context", 200}, {"GET", "/api/v1/regional-context?x=1", 400}, {"POST", "/api/v1/regional-context", 405}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.want || w.Body.Len() > 4096 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
