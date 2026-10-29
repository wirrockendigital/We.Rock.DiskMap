package security

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"testing"
)

// TestNodeMembership cannot substitute another snapshot, source, volume or virtual group.
func TestNodeMembership(t *testing.T) {
	share := "share"
	node := contracts.Node{SnapshotID: "snapshot", ID: "1", Kind: "file", VolumeID: "volume", ShareID: &share}
	if ValidateNode(node, "snapshot", node.ID, share, "volume", "dir/file") != nil {
		t.Fatal("valid membership rejected")
	}
	for _, change := range []func(*contracts.Node){func(n *contracts.Node) { n.SnapshotID = "other" }, func(n *contracts.Node) { n.VolumeID = "other" }, func(n *contracts.Node) { n.Kind = "virtual" }, func(n *contracts.Node) { other := "other"; n.ShareID = &other }} {
		copy := node
		change(&copy)
		if ValidateNode(copy, "snapshot", node.ID, share, "volume", "dir/file") == nil {
			t.Fatal("foreign membership accepted")
		}
	}
	if ValidateNode(node, "snapshot", node.ID, share, "volume", "../outside") == nil {
		t.Fatal("traversal accepted")
	}
}
