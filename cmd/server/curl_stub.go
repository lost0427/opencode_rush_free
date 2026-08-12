//go:build !linux || !cgo

package main

import "net/http"

// Non-container development builds retain the standard transport. Production
// Linux images compile curl_linux.go and route upstream traffic through libcurl.
func curlHTTPClient(ProxyRecord) (*http.Client, interface{ CloseIdleConnections() }, bool, error) {
	return nil, nil, false, nil
}
