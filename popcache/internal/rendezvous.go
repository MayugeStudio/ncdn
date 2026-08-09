package internal

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"log"
	"net/http"
)

var md5func = md5.New()

func rendezvousHash(s string) int {
	hash := md5.Sum([]byte(s))
	val := hash[:]
	sum := binary.BigEndian.Uint32(val)
	return int(sum)
}

// RendezvousSelect selects backends using rendezvous hashing
func RendezvousSelect(r *http.Request, backends []*Backend) *Backend {
	if len(backends) == 0 {
		log.Fatalf("backends must have at least one backend")
	}
	key := r.Host + r.URL.Port() + r.URL.RequestURI()

	var res *Backend
	currentMax := 0

	for _, backend := range backends {
		hash := rendezvousHash(fmt.Sprintf("%s:%s", backend.NodeId, key))
		if currentMax < hash {
			currentMax = hash
			res = backend
		}
	}
	return res
}
