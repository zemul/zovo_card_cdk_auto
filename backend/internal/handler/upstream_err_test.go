package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "Client.Timeout exceeded while awaiting headers" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestUpstreamTransportErrHidesUpstreamURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		err  error
		code string
	}{
		{&url.Error{Op: "Post", URL: "https://upstream.example/api/v1/cdk/preflight", Err: timeoutErr{}}, "upstream_timeout"},
		{&url.Error{Op: "Post", URL: "https://upstream.example/api/v1/cdk/preflight", Err: errors.New("connection refused")}, "upstream_unavailable"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		writeUpstreamTransportErr(c, "test", tc.err)
		body := w.Body.String()
		if w.Code != http.StatusBadGateway {
			t.Fatalf("status=%d", w.Code)
		}
		if strings.Contains(body, "upstream.example") || strings.Contains(body, "http") {
			t.Fatalf("upstream URL leaked: %s", body)
		}
		if !strings.Contains(body, tc.code) {
			t.Fatalf("want %s, got %s", tc.code, body)
		}
	}
}
