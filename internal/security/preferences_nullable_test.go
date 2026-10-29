package security

import (
	"strings"
	"testing"
)

// Nullable nested DTOs accept null without weakening required objects or exact nested fields.
func TestNullablePreferenceStructDecode(t *testing.T) {
	type nested struct {
		Name string `json:"name"`
	}
	for _, body := range []string{`{"value":null}`, `{"value":{"name":"known"}}`} {
		var v struct {
			Value *nested `json:"value"`
		}
		if e := DecodeJSON(strings.NewReader(body), 1024, &v); e != nil {
			t.Fatal(body, e)
		}
	}
	for _, body := range []string{`{"value":{"Name":"unknown"}}`, `{"value":{"name":"a","extra":true}}`, `{"value":{"name":"a","name":"b"}}`} {
		var v struct {
			Value *nested `json:"value"`
		}
		if e := DecodeJSON(strings.NewReader(body), 1024, &v); e == nil {
			t.Fatal(body)
		}
	}
	var required struct {
		Value nested `json:"value"`
	}
	if DecodeJSON(strings.NewReader(`{"value":null}`), 1024, &required) == nil {
		t.Fatal("required struct accepted null")
	}
}
