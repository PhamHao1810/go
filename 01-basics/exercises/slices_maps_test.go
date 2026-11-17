package exercises

import (
	"maps"
	"slices"
	"testing"
)

func TestWordCount(t *testing.T) {
	got := WordCount("Go go  NODE go")
	want := map[string]int{"go": 3, "node": 1}
	if !maps.Equal(got, want) {
		t.Errorf("WordCount() = %v, muốn %v", got, want)
	}
}

func TestReverse(t *testing.T) {
	in := []int{1, 2, 3}
	got := Reverse(in)
	if !slices.Equal(got, []int{3, 2, 1}) {
		t.Errorf("Reverse() = %v, muốn [3 2 1]", got)
	}
	if !slices.Equal(in, []int{1, 2, 3}) {
		t.Errorf("Reverse() đã sửa slice gốc thành %v", in)
	}
}

// Table-driven test: kiểu viết test phổ biến nhất trong Go.
func TestUnique(t *testing.T) {
	tests := []struct {
		name     string
		in, want []string
	}{
		{"rỗng", nil, nil},
		{"không trùng", []string{"a", "b"}, []string{"a", "b"}},
		{"có trùng", []string{"a", "b", "a", "c", "b"}, []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Unique(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("Unique(%v) = %v, muốn %v", tt.in, got, tt.want)
			}
		})
	}
}
