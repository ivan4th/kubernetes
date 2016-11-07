/*
Copyright 2014 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package filters

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"strings"

	"github.com/golang/glog"
)

func maybeIndentJson(bs []byte, header http.Header) ([]byte, error) {
	if !strings.HasPrefix(header.Get("Content-Type"), "application/json") {
		return bs, nil
	}
	var content interface{}
	if err := json.Unmarshal(bs, &content); err != nil {
		return bs, err
	}
	if indented, err := json.MarshalIndent(content, "", "  "); err != nil {
		return bs, err
	} else {
		return indented, nil
	}
}

type httpRecorder struct {
	inner *httptest.ResponseRecorder
	w     http.ResponseWriter
}

func (r *httpRecorder) Header() http.Header {
	return r.inner.Header()
}

func (r *httpRecorder) updateHeaders() {
	headers := r.w.Header()
	for k, v := range r.inner.Header() {
		headers[k] = v
	}
}

func (r *httpRecorder) Write(bs []byte) (int, error) {
	r.updateHeaders()
	n, err := r.inner.Write(bs)
	if err != nil {
		return n, err
	}
	return r.w.Write(bs)
}

func (r *httpRecorder) Flush() {
	if flusher, ok := r.w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *httpRecorder) WriteHeader(code int) {
	r.updateHeaders()
	r.inner.WriteHeader(code)
	r.w.WriteHeader(code)
}

// Hijack implements http.Hijacker.
func (r *httpRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	r.inner.WriteString("\n<hijacked>\n")
	return r.w.(http.Hijacker).Hijack()
}

// CloseNotify implements http.CloseNotifier
func (r *httpRecorder) CloseNotify() <-chan bool {
	return r.w.(http.CloseNotifier).CloseNotify()
}

type httpDump struct {
	handler http.Handler
	path    string
}

// func (dump *httpDump)
func (dump *httpDump) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := &httpRecorder{httptest.NewRecorder(), w}
	reqDump, err := httputil.DumpRequest(r, true)
	if err != nil {
		glog.V(0).Infof("DumpRequest error: %s", err)
	}
	parts := bytes.SplitN(reqDump, []byte("\r\n\r\n"), 2)
	if len(parts) == 2 {
		if indented, err := maybeIndentJson(parts[1], r.Header); err != nil {
			glog.V(2).Infof("error indenting request json: %s", err)
		} else {
			reqDump = append(append(parts[0], []byte("\r\n\r\n")...), indented...)
		}
	}

	dump.handler.ServeHTTP(rec, r)

	if indented, err := maybeIndentJson(rec.inner.Body.Bytes(), rec.inner.Header()); err != nil {
		glog.V(2).Infof("error indenting response json: %s", err)
	} else {
		rec.inner.Body = bytes.NewBuffer(indented)
	}

	respDump, err := httputil.DumpResponse(rec.inner.Result(), true)
	if err != nil {
		glog.V(0).Infof("DumpResponse error: %s", err)
		return
	}
	text := fmt.Sprintf("---- REQUEST ----\n%s\n---- RESPONSE ----\n%s\n---- END ----\n\n",
		reqDump, respDump)

	f, err := os.OpenFile(dump.path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		glog.V(0).Infof("dumpRequestAndResponse: error writing log file: %s", err)
		return
	}

	defer f.Close()

	_, err = f.WriteString(text)
	if err != nil {
		glog.V(0).Infof("dumpRequestAndResponse: error writing log file: %s", err)
		return
	}
}

func WithDump(h http.Handler, path string) http.Handler {
	glog.V(0).Infof("httpDump: setting up dumping to path: %s", path)
	return &httpDump{h, path}
}
