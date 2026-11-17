// Package middleware: các lớp bọc handler xử lý việc chung cho mọi request
// (tương đương Middleware / Interceptor / Guard của NestJS).
package middleware

import "net/http"

type Middleware func(http.Handler) http.Handler

// Chain(h, A, B) => request đi qua A -> B -> h.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
