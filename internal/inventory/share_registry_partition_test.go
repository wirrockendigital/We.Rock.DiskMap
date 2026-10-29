package inventory

import "testing"

// Offline USB records must not block internal shares or relax their identity checks.
func TestInternalRegistryExcludesUSBWithoutRelaxingNativeBindings(t *testing.T) {
	mappings := map[string]string{"data": "/volume1/data", "usbshare1": "/volumeUSB1/usbshare"}
	rights := map[string]ShareRights{"data": {Path: "/volume1/data"}, "serial-a|": {Path: "/volumeUSB1/usbshare"}, "serial-b|": {Path: "/volumeUSB1/usbshare"}}
	m, r, e := internalShareRegistry(mappings, rights)
	if e != nil || len(m) != 1 || len(r) != 1 || r["data"].Path != "/volume1/data" {
		t.Fatalf("native partition: %v %v %v", m, r, e)
	}
	for _, path := range []string{"/volume2/data", "/volumeUSBx/usbshare", "/volumeUSB1/usbshare/nested", "/volumeUSB1/usbshare/../data"} {
		_, _, e = internalShareRegistry(mappings, map[string]ShareRights{"data": {Path: path}})
		if e == nil {
			t.Fatalf("accepted mismatched native path %q", path)
		}
	}
	if _, _, e = internalShareRegistry(map[string]string{}, map[string]ShareRights{"data": {Path: "/volume1/data"}}); e == nil {
		t.Fatal("accepted missing native SMB mapping")
	}
	if _, _, e = internalShareRegistry(map[string]string{}, map[string]ShareRights{"data": {Path: "/volume1/data", Encrypted: true, Locked: true}}); e != nil {
		t.Fatal("rejected locked native share")
	}
}
