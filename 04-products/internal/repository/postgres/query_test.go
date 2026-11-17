package postgres

import (
	"errors"
	"strings"
	"testing"

	"golearn/04-products/internal/domain"
)

func TestPrefixQuery(t *testing.T) {
	cases := map[string]string{
		"Áo  thun!":         "Áo:* & thun:*",
		"điện thoại 15":     "điện:* & thoại:* & 15:*",
		"a & b | !(c)':*\\": "a:* & b:* & c:*",
		"!!!":               "",
	}
	for in, want := range cases {
		if got := prefixQuery(in); got != want {
			t.Errorf("prefixQuery(%q) = %q, muốn %q", in, got, want)
		}
	}
}

func TestCursor_RejectsTamperedOrMismatched(t *testing.T) {
	good := encodeCursor(cursor{Sort: string(domain.SortPriceAsc), Value: "100", ID: 5})
	if _, err := decodeCursor(good, domain.SortPriceAsc); err != nil {
		t.Fatalf("cursor hợp lệ bị từ chối: %v", err)
	}
	for name, c := range map[string]string{
		"không phải base64":    "%%%",
		"sai sort":             good, // cursor của sort=price dùng cho sort=-created_at
		"offset vượt giới hạn": encodeCursor(cursor{Sort: string(domain.SortRelevance), Offset: maxRanked}),
	} {
		if _, err := decodeCursor(c, domain.SortNewest); !errors.Is(err, domain.ErrInvalidCursor) {
			t.Errorf("%s: err = %v, muốn ErrInvalidCursor", name, err)
		}
	}

	_, err := buildListQuery(domain.ListFilter{Sort: domain.SortPriceAsc, Limit: 10,
		Cursor: encodeCursor(cursor{Sort: string(domain.SortPriceAsc), Value: "abc"})})
	if !errors.Is(err, domain.ErrInvalidCursor) {
		t.Errorf("cursor có giá trị không phải số: err = %v", err)
	}
}

func TestBuildListQuery_UsesParamsNotUserInput(t *testing.T) {
	min, max := int64(10), int64(99)
	q, err := buildListQuery(domain.ListFilter{Query: "áo'; DROP TABLE products;--", MinPrice: &min, MaxPrice: &max,
		InStock: true, Sort: domain.SortPriceDesc, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q.sql, "DROP") || strings.Contains(q.sql, "áo") {
		t.Errorf("giá trị người dùng lọt vào SQL: %s", q.sql)
	}
	for _, want := range []string{"price >= $2", "price <= $3", "stock > 0", "ORDER BY price DESC, id DESC", "LIMIT $4"} {
		if !strings.Contains(q.sql, want) {
			t.Errorf("SQL thiếu %q:\n%s", want, q.sql)
		}
	}
	if q, _ := buildListQuery(domain.ListFilter{Query: "!!!", Sort: domain.SortRelevance, Limit: 5}); !q.empty {
		t.Error("q không có chữ nào phải trả rỗng")
	}
}
