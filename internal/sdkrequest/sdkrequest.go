package sdkrequest

import (
	"net/http"
	"strings"
)

const (
	HeaderName  = "X-DuckBug-Internal"
	HeaderValue = "1"
)

func IsInternalRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.Header.Get(HeaderName)) == HeaderValue
}
