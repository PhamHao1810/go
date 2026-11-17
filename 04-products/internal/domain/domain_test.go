package domain

import (
	"errors"
	"testing"
)

func ptr(n int64) *int64 { return &n }

func fieldsOf(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		return nil
	}
	var de *Error
	if !errors.As(err, &de) || de.Code != "VALIDATION_FAILED" {
		t.Fatalf("err = %v, muốn VALIDATION_FAILED", err)
	}
	var out []string
	for _, f := range de.Fields {
		out = append(out, f.Field)
	}
	return out
}

func TestProductValidate_CollectsAllFields(t *testing.T) {
	got := fieldsOf(t, Product{Price: -1, Stock: -1}.Validate())
	want := []string{"sku", "name", "price", "stock"}
	if len(got) != len(want) {
		t.Fatalf("fields = %v, muốn %v", got, want)
	}
	if err := (Product{SKU: "A", Name: "Áo", Price: 1}).Validate(); err != nil {
		t.Errorf("product hợp lệ bị báo lỗi: %v", err)
	}
}

func TestListFilterNormalize(t *testing.T) {
	cases := []struct {
		name       string
		in         ListFilter
		wantSort   Sort
		wantFields []string
	}{
		{"mặc định", ListFilter{}, SortNewest, nil},
		{"có q thì sort theo liên quan", ListFilter{Query: " áo "}, SortRelevance, nil},
		{"relevance cần q", ListFilter{Sort: SortRelevance}, SortRelevance, []string{"sort"}},
		{"sort lạ", ListFilter{Sort: "name"}, "name", []string{"sort"}},
		{"limit quá lớn", ListFilter{Limit: 101}, SortNewest, []string{"limit"}},
		{"khoảng giá ngược", ListFilter{MinPrice: ptr(10), MaxPrice: ptr(5)}, SortNewest, []string{"max_price"}},
		{"giá âm", ListFilter{MinPrice: ptr(-1)}, SortNewest, []string{"min_price"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := c.in
			got := fieldsOf(t, f.Normalize())
			if f.Sort != c.wantSort || len(got) != len(c.wantFields) {
				t.Errorf("sort=%q fields=%v, muốn sort=%q fields=%v", f.Sort, got, c.wantSort, c.wantFields)
			}
		})
	}
}
