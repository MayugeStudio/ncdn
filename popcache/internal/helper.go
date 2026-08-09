package internal

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"github.com/yzp0n/ncdn/types"
)

func RemovePortFromHost(hostname string) string {
	outHost, _, err := net.SplitHostPort(hostname)
	var addrErr *net.AddrError
	if err != nil && errors.As(err, &addrErr) && addrErr.Err == "missing port in address" {
		return hostname
	}
	return outHost
}

func ParseBackends(configPath string, transport *http.Transport) ([]*types.Backend, error) {
	f, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load configs from %s", configPath)
	}
	defer f.Close()

	data := []struct {
		NodeId   string `json:"nodeId"`
		Ip4      string `json:"ip4"`
		Hostname string `json:"hostname"`
		Port     string `json:"openPort"`
	}{}

	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to parse configs %s: %w", configPath, err)
	}

	out := []*types.Backend{}
	for i := range data {
		ip4 := netip.MustParseAddr(data[i].Ip4)
		hostname := strings.ToLower(data[i].Hostname)
		urlStr := "http://" + data[i].Ip4 + ":" + data[i].Port
		u, err := url.Parse(urlStr)
		if err != nil {
			log.Fatalf("Failed to parse url: %s\n", urlStr)
		}
		out = append(out, &types.Backend{
			NodeId:    data[i].NodeId,
			Ip4:       ip4,
			Hostname:  hostname,
			Port:      data[i].Port,
			Url:       u,
			Transport: transport,
		})
	}

	return out, nil
}

