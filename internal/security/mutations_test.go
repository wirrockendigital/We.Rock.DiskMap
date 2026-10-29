package security

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	_ "modernc.org/sqlite"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestExactNestedSchemas rejects Go's case-insensitive aliases at every object depth.
func TestExactNestedSchemas(t *testing.T) {
	type nested struct {
		Item  JobActionRequest   `json:"item"`
		Items []JobActionRequest `json:"items"`
	}
	for _, raw := range []string{`{"item":{"JobId":"x"}}`, `{"item":{"jobId":"x","JobID":"y"}}`, `{"items":[{"unknown":1}]}`, `{"Item":{}}`} {
		var value nested
		if DecodeJSON(strings.NewReader(raw), 4096, &value) == nil {
			t.Fatal("accepted ambiguous schema", raw)
		}
	}
	var valid nested
	if DecodeJSON(strings.NewReader(`{"item":{"contractVersion":1,"jobId":"x","action":"pause"}}`), 4096, &valid) != nil {
		t.Fatal("rejected valid nested schema")
	}
}

// TestMutationActionContracts drives the production guard with local owner callbacks only.
func TestMutationActionContracts(t *testing.T) {
	for _, action := range []string{"pause", "resume", "cancel", "cancel-queued"} {
		for _, origin := range []string{"https://dsm.test:5001", "https://foreign.test"} {
			calls := 0
			identity := func(context.Context) (string, error) { return "admin", nil }
			handler := Protect(identity, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input JobActionRequest
				Mutate(w, r, "https://dsm.test:5001", identity, &input, func(principal, key string) {
					calls++
					if principal != "admin" || key != "key" || input.Action != action {
						t.Fatal("wrong command")
					}
					w.WriteHeader(204)
				})
			}))
			r := httptest.NewRequest("POST", "/action", strings.NewReader(`{"contractVersion":1,"jobId":"j1","action":"`+action+`"}`))
			r.Header.Set("Origin", origin)
			r.Header.Set("X-SYNO-TOKEN", "synthetic")
			r.Header.Set("Idempotency-Key", "key")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			expected := 1
			if origin != "https://dsm.test:5001" {
				expected = 0
			}
			if calls != expected {
				t.Fatal(action, origin, calls, w.Code)
			}
		}
	}
	for _, operation := range []string{"create", "update", "duplicate", "deactivate", "delete"} {
		input := ProfileMutationRequest{ContractVersion: 1, Operation: operation}
		if operation != "create" {
			input.ProfileID = "p1"
			input.ExpectedRevision = 1
		}
		input.ConfirmOwnDataDeletion = operation == "delete"
		if input.Validate() != nil {
			t.Fatal(operation)
		}
		if operation == "delete" {
			input.ConfirmOwnDataDeletion = false
			if input.Validate() == nil {
				t.Fatal("unconfirmed delete accepted")
			}
		}
	}
	if (JobActionRequest{1, "../outside", "pause"}).Validate() == nil {
		t.Fatal("invalid identity accepted")
	}
}

// TestSearchTextRemainsSQLData exercises real SQLite parameter binding and bounded inputs.
func TestSearchTextRemainsSQLData(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE nodes(snapshot_id TEXT,name TEXT);`); err != nil {
		t.Fatal(err)
	}
	text := `' OR 1=1; DROP TABLE nodes; --`
	for _, name := range []string{text, "ordinary"} {
		if _, err = db.Exec(`INSERT INTO nodes VALUES(?,?)`, "s1", name); err != nil {
			t.Fatal(err)
		}
	}
	input := SearchInput{SnapshotID: "s1", Text: text, Limit: 50}
	if input.Validate() != nil {
		t.Fatal("SQL-like literal rejected")
	}
	rows, err := db.Query(`SELECT name FROM nodes WHERE snapshot_id=? AND instr(name,?)>0 LIMIT ?`, input.SnapshotID, input.Text, input.Limit)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		var got string
		if rows.Scan(&got) != nil || got != text {
			t.Fatal("query escaped binding")
		}
		count++
	}
	rows.Close()
	if count != 1 {
		t.Fatal(count)
	}
	var total int
	if db.QueryRow(`SELECT count(*) FROM nodes`).Scan(&total) != nil || total != 2 {
		t.Fatal("source modified")
	}
	input.Text = strings.Repeat("x", 4097)
	if input.Validate() == nil {
		t.Fatal("unbounded search")
	}
}

// TestUnavailableOwnerAndRevocation never acknowledge an unimplemented feature or stale session.
func TestUnavailableOwnerAndRevocation(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		valid := true
		identity := func(context.Context) (string, error) {
			if !valid {
				return "other", nil
			}
			return "admin", nil
		}
		handler := Protect(identity, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var input JobActionRequest
			Mutate(w, r, "https://dsm.test", identity, &input, nil)
		}))
		body := `{"contractVersion":1,"jobId":"j1","action":"cancel"}`
		reader := &mutationReader{Reader: strings.NewReader(body), after: func() {
			if revoked {
				valid = false
			}
		}}
		r := httptest.NewRequest("POST", "/action", reader)
		r.Header.Set("Origin", "https://dsm.test")
		r.Header.Set("X-SYNO-TOKEN", "synthetic")
		r.Header.Set("Idempotency-Key", "key")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := 503
		if revoked {
			want = 401
		}
		if w.Code != want {
			t.Fatal(revoked, w.Code)
		}
	}
}

// mutationReader models identity changes while a client body is still being read.
type mutationReader struct {
	io.Reader
	after func()
}

func (r *mutationReader) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	r.after()
	return n, e
}

// TestProfileMutationBoundary uses the shared production guard for every profile precondition contract.
func TestProfileMutationBoundary(t *testing.T) {
	for _, operation := range []string{"create", "update", "duplicate", "deactivate", "delete"} {
		input := ProfileMutationRequest{ContractVersion: 1, Operation: operation}
		if operation != "create" {
			input.ProfileID = "p"
			input.ExpectedRevision = 1
		}
		input.ConfirmOwnDataDeletion = operation == "delete"
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		for _, origin := range []string{"https://dsm.test", "https://foreign.test"} {
			calls := 0
			identity := func(context.Context) (string, error) { return "admin", nil }
			handler := Protect(identity, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var value ProfileMutationRequest
				Mutate(w, r, "https://dsm.test", identity, &value, func(string, string) { calls++; w.WriteHeader(204) })
			}))
			r := httptest.NewRequest("POST", "/action", bytes.NewReader(raw))
			r.Header.Set("Origin", origin)
			r.Header.Set("X-SYNO-TOKEN", "synthetic")
			r.Header.Set("Idempotency-Key", "key")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			want := 1
			if origin != "https://dsm.test" {
				want = 0
			}
			if calls != want {
				t.Fatal(operation, calls, w.Code)
			}
		}
	}
}
