package app

import (
	"crypto/tls"
	"net/http"
	"time"
)

// This client is exclusive to fetchOriginalMetadataImage. Ordinary asset image
// consumers keep assetImageClient byte-for-byte unchanged. Its default proxy
// selection, 20-second timeout and no-redirect policy are retained; URL checks,
// the 8 MiB bound and authorized sidecar publication stay in the existing caller.
var originalMetadataImageClient = newOriginalMetadataImageClient()

func newOriginalMetadataImageClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.InsecureSkipVerify = true
	return &http.Client{
		Timeout:       20 * time.Second,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
