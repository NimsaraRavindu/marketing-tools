// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ETag buffers a successful GET response, derives a strong ETag from its body,
// sets Cache-Control, and answers a matching If-None-Match with 304 Not
// Modified (empty body). This gives React Query + the HTTP cache real
// validators, so the frontend's bespoke IndexedDB layer and its uncoordinated
// invalidation mechanisms (FE.md 4) can be retired in favor of conditional
// requests. Only 200 GET responses are validated; everything else passes
// through untouched.
func ETag(cacheControl string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet {
			c.Next()
			return
		}

		buf := &bufferingWriter{ResponseWriter: c.Writer, body: &bytes.Buffer{}, status: http.StatusOK}
		c.Writer = buf
		c.Next()
		c.Writer = buf.ResponseWriter

		// Only bodies of successful responses get a validator; error bodies and
		// empty responses are written through as-is.
		if buf.status != http.StatusOK || buf.body.Len() == 0 {
			buf.flush()
			return
		}

		sum := sha256.Sum256(buf.body.Bytes())
		etag := `"` + hex.EncodeToString(sum[:]) + `"`
		c.Writer.Header().Set("ETag", etag)
		if cacheControl != "" {
			c.Writer.Header().Set("Cache-Control", cacheControl)
		}

		if match := c.Request.Header.Get("If-None-Match"); match == etag {
			c.Writer.Header().Del("Content-Length")
			c.Writer.WriteHeader(http.StatusNotModified)
			return
		}

		buf.flush()
	}
}

// bufferingWriter captures the handler's response so the ETag middleware can
// hash the body and decide between 304 and the full body before anything is
// sent to the client.
type bufferingWriter struct {
	gin.ResponseWriter
	body    *bytes.Buffer
	status  int
	written bool
}

func (w *bufferingWriter) WriteHeader(status int) {
	w.status = status
	w.written = true
}

func (w *bufferingWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}

func (w *bufferingWriter) WriteString(s string) (int, error) {
	return w.body.WriteString(s)
}

// flush writes the buffered status and body through to the real writer. Called
// once the middleware has decided not to short-circuit with a 304.
func (w *bufferingWriter) flush() {
	w.ResponseWriter.WriteHeader(w.status)
	if w.body.Len() > 0 {
		_, _ = w.ResponseWriter.Write(w.body.Bytes())
	}
}
