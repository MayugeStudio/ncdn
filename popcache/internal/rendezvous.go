package internal

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"log"
	"net/http"
	"github.com/yzp0n/ncdn/types"
)

var md5func = md5.New()

func rednezvousHash(s string) int {
	hash := md5.Sum([]byte(s))
	val := hash[:]
	sum := binary.BigEndian.Uint32(val)
	return int(sum)
}

// RendezvousSelect selects upstreams using rendezvous hashing
func RendezvousSelect(r *http.Request, upstreams []*types.Upstream) *types.Upstream {
	if len(upstreams) == 0 {
		log.Fatalf("upstreams must have at least one upstream")
	}
	key := r.Host + r.URL.Port() + r.URL.RequestURI()

	var res *types.Upstream
	currentMax := 0

	for _, upstream := range upstreams {
		hash := rednezvousHash(fmt.Sprintf("%s:%s", upstream.NodeId, key))
		if currentMax < hash {
			currentMax = hash
			res = upstream
		}
	}
	return res
}

