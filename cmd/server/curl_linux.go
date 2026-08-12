//go:build linux && cgo

package main

/*
#cgo pkg-config: libcurl
#include <curl/curl.h>
#include <stdlib.h>
#include <string.h>
typedef struct { char *p; size_t n; } buf;
static size_t cb(char *p,size_t s,size_t n,void *u){buf*b=u;size_t z=s*n;char*q=realloc(b->p,b->n+z);if(!q)return 0;b->p=q;memcpy(q+b->n,p,z);b->n+=z;return z;}
static CURLcode run(const char*u,const char*m,const char*b,const char*p,const char*a,const char*extra,long t,buf*out,long*st){CURL*c=curl_easy_init();if(!c)return CURLE_FAILED_INIT;struct curl_slist*h=NULL;h=curl_slist_append(h,"Accept: application/json");h=curl_slist_append(h,"X-Opencode-Client: cli");h=curl_slist_append(h,"Content-Type: application/json");if(extra){const char*x=extra;while(*x){const char*e=strchr(x,'\n');size_t n=e?(size_t)(e-x):strlen(x);char*line=malloc(n+1);memcpy(line,x,n);line[n]=0;h=curl_slist_append(h,line);free(line);if(!e)break;x=e+1;}}curl_easy_setopt(c,CURLOPT_URL,u);curl_easy_setopt(c,CURLOPT_CUSTOMREQUEST,m);curl_easy_setopt(c,CURLOPT_POSTFIELDS,b);curl_easy_setopt(c,CURLOPT_HTTPHEADER,h);curl_easy_setopt(c,CURLOPT_USERAGENT,"opencode/1.18.12 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.13");curl_easy_setopt(c,CURLOPT_WRITEFUNCTION,cb);curl_easy_setopt(c,CURLOPT_WRITEDATA,out);curl_easy_setopt(c,CURLOPT_TIMEOUT_MS,t);curl_easy_setopt(c,CURLOPT_HTTP_VERSION,CURL_HTTP_VERSION_2TLS);if(p&&*p)curl_easy_setopt(c,CURLOPT_PROXY,p);if(a&&*a)curl_easy_setopt(c,CURLOPT_PROXYUSERPWD,a);CURLcode e=curl_easy_perform(c);curl_easy_getinfo(c,CURLINFO_RESPONSE_CODE,st);curl_slist_free_all(h);curl_easy_cleanup(c);return e;}
*/
import "C"
import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unsafe"
)

type curlTransport struct{ proxy, auth string }

func (t *curlTransport) CloseIdleConnections() {}
func (t *curlTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
	}
	u, m, b := C.CString(r.URL.String()), C.CString(r.Method), C.CString(string(body))
	defer C.free(unsafe.Pointer(u))
	defer C.free(unsafe.Pointer(m))
	defer C.free(unsafe.Pointer(b))
	var p, a *C.char
	if t.proxy != "" {
		p = C.CString(t.proxy)
		defer C.free(unsafe.Pointer(p))
	}
	if t.auth != "" {
		a = C.CString(t.auth)
		defer C.free(unsafe.Pointer(a))
	}
	var out C.buf
	var st C.long
	var extra strings.Builder
	for k, values := range r.Header {
		for _, v := range values {
			if !strings.EqualFold(k, "User-Agent") && !strings.EqualFold(k, "Content-Length") {
				extra.WriteString(k)
				extra.WriteString(": ")
				extra.WriteString(v)
				extra.WriteByte('\n')
			}
		}
	}
	x := C.CString(extra.String())
	defer C.free(unsafe.Pointer(x))
	result := make(chan C.CURLcode, 1)
	go func() { result <- C.run(u, m, b, p, a, x, 120000, &out, &st) }()
	select {
	case e := <-result:
		if e != C.CURLE_OK {
			return nil, fmt.Errorf("curl: %s", C.GoString(C.curl_easy_strerror(e)))
		}
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
	data := C.GoBytes(unsafe.Pointer(out.p), C.int(out.n))
	C.free(unsafe.Pointer(out.p))
	return &http.Response{StatusCode: int(st), Status: fmt.Sprintf("%d %s", st, http.StatusText(int(st))), Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
}
func curlHTTPClient(p ProxyRecord) (*http.Client, interface{ CloseIdleConnections() }, bool, error) {
	if strings.TrimSpace(p.URI) == "" {
		return nil, nil, false, nil
	}
	a := ""
	if p.Username != "" {
		a = p.Username + ":" + p.Password
	}
	t := &curlTransport{proxy: p.URI, auth: a}
	return &http.Client{Transport: t}, t, true, nil
}
