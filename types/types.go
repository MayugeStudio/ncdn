package types

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/netip"
	"net/url"
)

type PoPInfo struct {
	// The PoP identifier for convenience
	Id string

	// The IPv4 address of the PoP
	Ip4 netip.Addr

	// The URL fetched by probers to measure latency
	LatencyEndpointUrl string

	// [webui] CSS of the region popup
	UIPopupCSS string
}

type RegionInfo struct {
	// The region identifier for convenience
	Id string

	// IPv4 Prefixes constituting the user region
	Prefixes []netip.Prefix

	// The prober that we will use to represent the region
	ProberURL string

	// [webui] CSS of the region popup
	UIPopupCSS string
}

type PoPStatus struct {
	Id     string  `json:"id"`
	Uptime float64 `json:"uptime"`
	Load   float64 `json:"load"`
	Error  string  `json:"error,omitempty"`
}

type ProbeArgs struct {
	TargetUrl string `json:"target_url"`
}

type ProbeResult struct {
	ProberNodeId string `json:"prober_node_id"`
	Url          string `json:"url"`
	Start        int64  `json:"start"`
	DNSEnd       int64  `json:"dns_end"`
	ConnectEnd   int64  `json:"connect_end"`
	RequestEnd   int64  `json:"request_end"`
	FirstByte    int64  `json:"first_byte"`
	ResponseEnd  int64  `json:"response_end"`
	ResponseCode int    `json:"response_code"`
}

type Object struct {
	StatusCode int
	Header     http.Header
	Body       []byte // Bodyをio.Reader的な感じのやつにしてもいいかも何回も読めるやつ
}

func (c *Object) Clone() *Object {
	return &Object{
		StatusCode: c.StatusCode,
		Header:     c.Header.Clone(),
		Body:       bytes.Clone(c.Body),
	}
}

// TODO: internalに移動
type Backend struct {
	NodeId   string
	Ip4      netip.Addr
	Hostname string // Hostnameいらないかも
	Port     string
	Url      *url.URL

	Transport *http.Transport
}

func (b *Backend) Fetch(ctx context.Context, r *http.Request) (*http.Response, error) {
	url := "http://" + b.Ip4.String() + ":" + b.Port + r.URL.RequestURI()
	newReq, err := http.NewRequestWithContext(ctx, r.Method, url, nil)
	if err != nil {
		return nil, err
	}

	newReq.Header = r.Header.Clone()
	newReq.Host = r.Host

	log.Printf("Send request to %s(%s)\n", b.NodeId, url)
	return b.Transport.RoundTrip(newReq)
}
