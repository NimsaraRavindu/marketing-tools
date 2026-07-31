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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func etagRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", ETag("private, max-age=60, must-revalidate"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"hello": "world"})
	})
	return r
}

func TestETag_SetsValidatorsAndCacheControl(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	etagRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("expected an ETag header")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, max-age=60, must-revalidate" {
		t.Errorf("Cache-Control = %q, want the configured value", cc)
	}
	if body := rec.Body.String(); body != `{"hello":"world"}` {
		t.Errorf("body = %q, want the full JSON on a fresh request", body)
	}
}

func TestETag_MatchingIfNoneMatchReturns304WithNoBody(t *testing.T) {
	// First request to learn the ETag.
	rec1 := httptest.NewRecorder()
	etagRouter().ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/x", nil))
	etag := rec1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag from first request")
	}

	// Second request with If-None-Match should 304 with an empty body.
	rec2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("If-None-Match", etag)
	etagRouter().ServeHTTP(rec2, req)

	if rec2.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec2.Code)
	}
	if rec2.Body.Len() != 0 {
		t.Errorf("304 body = %q, want empty", rec2.Body.String())
	}
	if rec2.Header().Get("ETag") != etag {
		t.Errorf("304 should still carry the ETag, got %q", rec2.Header().Get("ETag"))
	}
}

func TestETag_DifferentBodyProducesDifferentETag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	payload := "a"
	r.GET("/y", ETag(""), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"v": payload})
	})

	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/y", nil))
	payload = "b"
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/y", nil))

	if rec1.Header().Get("ETag") == rec2.Header().Get("ETag") {
		t.Error("expected different ETags for different bodies")
	}
}

func TestETag_NonGetIsUntouched(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/z", ETag("private, max-age=60"), func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"ok": true})
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/z", nil))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if rec.Header().Get("ETag") != "" {
		t.Error("non-GET should not get an ETag")
	}
}
