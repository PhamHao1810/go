package exercises

import "testing"

func TestStack(t *testing.T) {
	var s Stack[string] // zero value dùng được luôn, không cần constructor
	if _, ok := s.Pop(); ok {
		t.Fatal("Pop() trên stack rỗng phải trả ok=false")
	}
	s.Push("a")
	s.Push("b")
	if s.Len() != 2 {
		t.Fatalf("Len() = %d, muốn 2", s.Len())
	}
	if v, ok := s.Pop(); !ok || v != "b" {
		t.Errorf("Pop() = (%q, %v), muốn (\"b\", true)", v, ok)
	}
	if v, ok := s.Pop(); !ok || v != "a" {
		t.Errorf("Pop() = (%q, %v), muốn (\"a\", true)", v, ok)
	}
	if s.Len() != 0 {
		t.Errorf("Len() = %d, muốn 0", s.Len())
	}
}
