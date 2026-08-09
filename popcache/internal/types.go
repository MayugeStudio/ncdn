package internal

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/netip"
	"net/url"
)

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

