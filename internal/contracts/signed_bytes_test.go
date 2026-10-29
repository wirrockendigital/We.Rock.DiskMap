package contracts

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSignedBytesRoundtrip keeps negatives separate from the unchanged nonnegative size contract.
func TestSignedBytesRoundtrip(t *testing.T) {
	for _, s := range []string{"0", "-1", "9007199254740993", "-9007199254740993"} {
		b := SignedBytes(s)
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		var got SignedBytes
		if err = json.Unmarshal(raw, &got); err != nil || got != b {
			t.Fatal(got, err)
		}
	}
	for _, s := range []string{"", "-0", "+1", "01", "-01", "1e3", "1.2", strings.Repeat("1", 129)} {
		if SignedBytes(s).Validate() == nil {
			t.Fatal("invalid signed value accepted", s)
		}
	}
	for _, raw := range []string{"1", "null", "\"-0\""} {
		var got SignedBytes
		if json.Unmarshal([]byte(raw), &got) == nil {
			t.Fatal("invalid wire value accepted", raw)
		}
	}
	if Bytes("-1").Validate() == nil {
		t.Fatal("file-size contract weakened")
	}
}
