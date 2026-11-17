// Package logger: slog dạng JSON, tự gắn request_id vào MỌI dòng log ghi bằng ...Context(ctx, ...).
// Nhờ vậy 1 lỗi ở tầng service tra ngược được về đúng request (và client gửi request_id khi báo lỗi).
package logger

import (
	"context"
	"log/slog"
	"os"
)

type ctxKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

func New(level slog.Level) *slog.Logger {
	return slog.New(ctxHandler{slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})})
}

type ctxHandler struct{ slog.Handler }

func (h ctxHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h ctxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return ctxHandler{h.Handler.WithAttrs(attrs)}
}

func (h ctxHandler) WithGroup(name string) slog.Handler {
	return ctxHandler{h.Handler.WithGroup(name)}
}
