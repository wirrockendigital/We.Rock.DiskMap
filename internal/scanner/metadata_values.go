package scanner

import (
	"math"
	"math/big"
	"strconv"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// metadataBytes preserves unsupported negative readings as unknown and multiplies blocks exactly.
// Linux st_blocks uses 512-byte units independently of the filesystem's block size.
func metadataBytes(size, blocks int64) (*contracts.Bytes, *contracts.Bytes) {
	var logical, allocated *contracts.Bytes
	if size >= 0 {
		v := contracts.Bytes(strconv.FormatInt(size, 10))
		logical = &v
	}
	if blocks >= 0 {
		// Ordinary allocations fit uint64; retain exact arithmetic for extreme metadata.
		var v contracts.Bytes
		if uint64(blocks) <= math.MaxUint64/512 {
			v = contracts.Bytes(strconv.FormatUint(uint64(blocks)*512, 10))
		} else {
			n := new(big.Int).Lsh(big.NewInt(blocks), 9)
			v = contracts.Bytes(n.String())
		}
		allocated = &v
	}
	return logical, allocated
}
