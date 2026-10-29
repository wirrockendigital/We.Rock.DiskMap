package jobs

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/sys/unix"
)

// filterCacheObject uses the platform adapter; Linux test binaries alone replace it for synthetic fixtures.
var filterCacheObject = nativeFilterCacheObject

var filterPersistentObject = regexp.MustCompile(`^btrfs:[0-9a-f]{32}:[1-9][0-9]*:[1-9][0-9]*$`)
var filterBootObject = regexp.MustCompile(`^darwin-boot:[0-9a-f]{32}:[0-9a-f]{16}:[1-9][0-9]*$`)

// validFilterCacheObject accepts only explicit v2 identity kinds, never old device/inode pairs.
func validFilterCacheObject(object string) bool {
	if !filterPersistentObject.MatchString(object) && !filterBootObject.MatchString(object) {
		return false
	}
	parts := strings.Split(object, ":")
	return parts[1] != strings.Repeat("0", 32) && parts[2] != strings.Repeat("0", 16)
}

// persistentFilterObject excludes ephemeral st_dev; held-descriptor runtime checks still require it.
func persistentFilterObject(filesystem string, st unix.Stat_t) string {
	object := fmt.Sprintf("%s:%d", filesystem, st.Ino)
	if !validFilterCacheObject(object) {
		return ""
	}
	return object
}
