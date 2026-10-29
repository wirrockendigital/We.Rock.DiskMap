package profiles

import (
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// TestActionStrictConfiguration proves nested case, physical-path, duplicate and exclusion rejection.
func TestActionStrictConfiguration(t *testing.T) {
	valid := `{"contractVersion":1,"operation":"create","configuration":{"name":"Profile","scope":{"kind":"volumes","targetIds":["volume"]},"active":true,"exclusions":[],"resourceMode":"gentle","limits":{"workers":1,"ioBytesPerSecond":null,"cpuPercent":null},"freshnessSeconds":60}}`
	var in ActionRequest
	if security.DecodeJSON(strings.NewReader(valid), 16384, &in) != nil || in.Validate() != nil {
		t.Fatal("valid action rejected")
	}
	for _, bad := range []string{
		strings.Replace(valid, "targetIds", "TargetIds", 1), strings.Replace(valid, "workers", "Workers", 1), strings.Replace(valid, `"name":"Profile"`, `"name":"Profile","name":"Duplicate"`, 1), strings.Replace(valid, `"name":"Profile"`, `"name":"Profile","physicalPath":"/arbitrary"`, 1), strings.Replace(valid, `"exclusions":[]`, `"exclusions":[{"kind":"pattern","value":"../escape"}]`, 1), strings.Replace(valid, `"configuration":{`, `"configuration":{"id":"client-id",`, 1),
	} {
		var req ActionRequest
		if security.DecodeJSON(strings.NewReader(bad), 16384, &req) == nil && req.Validate() == nil {
			t.Fatal("unsafe schema accepted", bad)
		}
	}
}

// TestJobIntentContractDoesNotClaimFullFallback keeps unimplemented execution semantics explicit.
func TestJobIntentContractDoesNotClaimFullFallback(t *testing.T) {
	for _, op := range []string{"refresh", "rebuild"} {
		if (JobRequest{ContractVersion: 1, ProfileID: "p", ExpectedRevision: 1, Operation: op}).Validate() != nil {
			t.Fatal(op)
		}
	}
	if (JobRequest{ContractVersion: 1, ProfileID: "p", ExpectedRevision: 1, Operation: "full"}).Validate() == nil {
		t.Fatal("intent changed to full")
	}
}
