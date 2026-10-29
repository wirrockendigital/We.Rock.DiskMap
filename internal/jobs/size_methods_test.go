package jobs

import "testing"

// TestSizeMethodQualification requires the exact independently qualified native platform tuple.
func TestSizeMethodQualification(t *testing.T) {
	version := "buildnumber=\"90080\""
	info := "unique=\"synology_kvmx64_virtualdsm\""
	if len(qualifiedSizeMethods(version, info, "x86_64")) != 2 {
		t.Fatal("qualified platform rejected")
	}
	for _, v := range [][3]string{{"", info, "x86_64"}, {version, "", "x86_64"}, {version, info, "aarch64"}, {version + "\n" + version, info, "x86_64"}, {"buildnumber=\"90081\"", info, "x86_64"}, {version, "unique=\"synology_other\"", "x86_64"}} {
		if len(qualifiedSizeMethods(v[0], v[1], v[2])) != 1 {
			t.Fatal("unqualified method accepted", v)
		}
	}
}
