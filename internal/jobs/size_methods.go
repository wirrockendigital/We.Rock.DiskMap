package jobs

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"strings"
)

// qualifiedSizeMethods requires exact server-owned platform records and rejects duplicate keys.
func qualifiedSizeMethods(version, info, architecture string) []string {
	methods := []string{"descriptor-no-follow-stat"}
	exact := func(raw, key, want string) bool {
		count := 0
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, key+"=") {
				if line != key+"=\""+want+"\"" {
					return false
				}
				count++
			}
		}
		return count == 1
	}
	if architecture == "x86_64" && exact(version, "buildnumber", "90080") && exact(info, "unique", "synology_kvmx64_virtualdsm") {
		methods = append(methods, contracts.AllocatedQualification)
	}
	return methods
}
