package exercises

import (
	"strings"
)

// Bài 1: Đếm số lần xuất hiện của mỗi từ.
func WordCount(s string) map[string]int {
	result := make(map[string]int)

	words := strings.Fields(s)

	for _, word := range words {
		word = strings.ToLower(word)
		result[word]++
	}

	return result
}

// Bài 2: Đảo ngược slice, không sửa slice gốc.
func Reverse[T any](s []T) []T {
	result := make([]T, len(s))

	for i, value := range s {
		result[len(s)-1-i] = value
	}

	return result
}

// Bài 3: Loại bỏ phần tử trùng, giữ thứ tự ban đầu.
func Unique[T comparable](s []T) []T {
	seen := make(map[T]struct{})
	result := make([]T, 0, len(s))

	for _, value := range s {
		if _, exists := seen[value]; exists {
			continue
		}

		seen[value] = struct{}{}
		result = append(result, value)
	}

	return result
}
