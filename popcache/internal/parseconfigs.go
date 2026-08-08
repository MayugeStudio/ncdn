package internal

import (
	"encoding/json"
	"fmt"
	"log"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"github.com/yzp0n/ncdn/types"
)

func ParseUpstreams(configPath string) ([]*types.Upstream, error) {
	f, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load configs from %s", configPath)
	}
	defer f.Close()

	data := []struct{
		Ip4      string `json:"ip4"`
		Hostname string `json:"hostname"`
		Port     string `json:"openPort"`
	}{}

	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to parse configs %s: %w", configPath, err)
	}

	out := []*types.Upstream{}
	for i := range data {
		ip4 := netip.MustParseAddr(data[i].Ip4)
		hostname := strings.ToLower(data[i].Hostname)
		urlStr := "http://" + data[i].Ip4 + ":" +data[i].Port
		u, err := url.Parse(urlStr)
		if err != nil {
			log.Fatalf("Failed to parse url: %s\n", urlStr)
		}
		out = append(out, &types.Upstream{
			Ip4: ip4,
			Hostname: hostname,
			Port: data[i].Port,
			Url: u,
		})
	}

	return out, nil
}

