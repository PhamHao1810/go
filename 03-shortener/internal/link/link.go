// Package link là một "feature module": model + service + repository + handler
// (tương đương LinkModule trong NestJS).
package link

import (
	"errors"
	"time"
)

type Link struct {
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	Clicks    int64     `json:"clicks"`
	CreatedAt time.Time `json:"created_at"`
}

// Lỗi nghiệp vụ. Handler sẽ map chúng sang HTTP status (xem handler.go).
var (
	ErrNotFound   = errors.New("link not found")
	ErrCodeTaken  = errors.New("code already exists")
	ErrInvalidURL = errors.New("invalid url: must be http(s)://...")
)
