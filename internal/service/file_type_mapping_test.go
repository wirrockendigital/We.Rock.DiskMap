package service

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestMappingStrictPrivateBoundary rejects case variants, duplicate keys and oversized input.
func TestMappingStrictPrivateBoundary(t *testing.T) {
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	m, e := jobs.Open(context.Background(), filepath.Join(base, "state"))
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerJobs(mux, m)
	send := func(body string) int {
		r := httptest.NewRequest("PUT", "/api/v1/file-type-mapping", strings.NewReader(body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	valid := `{"contractVersion":1,"expectedRevision":1,"mapping":{"version":1,"revision":2,"assignments":[{"extension":"图像","category":"image"}]}}`
	for _, body := range []string{
		strings.Replace(valid, "category", "Category", 1), strings.Replace(valid, "mapping", "Mapping", 1),
		strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(valid, `"category":"image"`, `"category":"image","unknown":1`, 1),
		valid + "{}", strings.Repeat(" ", 32769) + valid,
	} {
		if got := send(body); got != 400 {
			t.Fatal(got)
		}
	}
	if got := send(valid); got != 200 {
		t.Fatal(got)
	}
	if got := send(valid); got != 409 {
		t.Fatal(got)
	}
}
