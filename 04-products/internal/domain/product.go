package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

type Product struct {
	ID          int64
	SKU         string
	Name        string
	Description string
	Price       int64 // VND, số nguyên để không sai số như float
	Stock       int64
	Version     int64 // optimistic locking
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Images      []Image // theo position tăng dần
}

const MaxProductImages = 10

func (p Product) Validate() error {
	var v Validator
	v.Check(p.SKU != "" && utf8.RuneCountInString(p.SKU) <= 64, "sku", "required, max 64 characters")
	v.Check(strings.TrimSpace(p.Name) != "" && utf8.RuneCountInString(p.Name) <= 200, "name", "required, max 200 characters")
	v.Check(utf8.RuneCountInString(p.Description) <= 5000, "description", "max 5000 characters")
	v.Check(p.Price >= 0, "price", "must be >= 0")
	v.Check(p.Stock >= 0, "stock", "must be >= 0")
	return v.Err()
}
