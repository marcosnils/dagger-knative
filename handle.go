package function

import (
	"context"
	"net/http"
)

// Handle an HTTP Request.
func Handle(ctx context.Context, rw http.ResponseWriter, req *http.Request) {
	rw.Write([]byte("hello function"))
}
