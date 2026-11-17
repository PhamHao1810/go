package exercises

type Stack[T any] struct {
	items []T
}

func (s *Stack[T]) Push(v T) {
	s.items = append(s.items, v)
}

func (s *Stack[T]) Pop() (T, bool) {
	if len(s.items) == 0 {
		var zero T
		return zero, false
	}

	lastIndex := len(s.items) - 1
	value := s.items[lastIndex]

	var zero T
	s.items[lastIndex] = zero
	s.items = s.items[:lastIndex]

	return value, true
}

func (s *Stack[T]) Len() int {
	return len(s.items)
}
