package service

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// RegionalContext exposes a qualified zone name without exposing system paths.
type RegionalContext struct {
	Version        int     `json:"version"`
	NASTimeZone    *string `json:"nasTimeZone"`
	TimeZoneSource string  `json:"timeZoneSource"`
}

// ValidRegionalContext rejects path-like values and contradictory availability.
func ValidRegionalContext(v RegionalContext) bool {
	if v.Version != 1 {
		return false
	}
	if v.NASTimeZone == nil {
		return v.TimeZoneSource == "unavailable"
	}
	return v.TimeZoneSource == "system-zoneinfo" && validZoneName(*v.NASTimeZone)
}

// validZoneName accepts only bounded IANA identifiers recognized by the runtime.
func validZoneName(name string) bool {
	if name == "" || len(name) > 128 || name == "Local" || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, c := range part {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '+') {
				return false
			}
		}
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

// regionalContext reads only a bounded zoneinfo file through a confined directory handle.
func regionalContext(localtime, zoneinfo string) RegionalContext {
	out := RegionalContext{Version: 1, TimeZoneSource: "unavailable"}
	target, err := os.Readlink(localtime)
	if err != nil || len(target) > 4096 {
		return out
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(localtime), target)
	}
	name, err := filepath.Rel(zoneinfo, filepath.Clean(target))
	if err != nil || !validZoneName(filepath.ToSlash(name)) {
		return out
	}
	root, err := os.OpenRoot(zoneinfo)
	if err != nil {
		return out
	}
	defer root.Close()
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return out
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return out
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return out
	}
	if _, err = time.LoadLocationFromTZData(name, data); err != nil {
		return out
	}
	out.NASTimeZone = &name
	out.TimeZoneSource = "system-zoneinfo"
	return out
}

// registerRegionalContext inherits the server's Unix peer guard and performs no mutation.
func registerRegionalContext(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/regional-context", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			jobError(w, jobs.ErrInvalid)
			return
		}
		boundedReply(w, regionalContext("/etc/localtime", "/usr/share/zoneinfo"))
	})
}
