package internal

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func RemovePortFromHost(hostname string) string {
	outHost, _, err := net.SplitHostPort(hostname)
	var addrErr *net.AddrError
	if err != nil && errors.As(err, &addrErr) && addrErr.Err == "missing port in address" {
		return hostname
	}
	return outHost
}

type Node struct {
		NodeId   string `yaml:"nodeId"`
		Ip4      string `yaml:"ip4"`
		Hostname string `yaml:"hostname"`
		Port     string `yaml:"openPort"`
}

type Config struct {
	Nodes []Node `yaml:"nodes"`
}

func ParseBackends(configPath string, transport *http.Transport) ([]*Backend, error) {
	f, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load configs from %s", configPath)
	}
	defer f.Close()

	var cfg Config

	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse configs %s: %w", configPath, err)
	}

	out := []*Backend{}
	for i := range cfg.Nodes  {
		ip4, err := netip.ParseAddr(cfg.Nodes[i].Ip4)
		if err != nil {
			log.Printf("Failed to parse address: %s\n", cfg.Nodes[i].Ip4)
			return nil, err
		}
		hostname := strings.ToLower(cfg.Nodes[i].Hostname)
		urlStr := "http://" + cfg.Nodes[i].Ip4 + ":" + cfg.Nodes[i].Port
		u, err := url.Parse(urlStr)
		if err != nil {
			log.Printf("Failed to parse url: %s\n", urlStr)
			return nil, err
		}
		out = append(out, &Backend{
			NodeId:    cfg.Nodes[i].NodeId,
			Ip4:       ip4,
			Hostname:  hostname,
			Port:      cfg.Nodes[i].Port,
			Url:       u,
			Transport: transport,
		})
	}

	return out, nil
}

