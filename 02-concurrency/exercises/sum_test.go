package exercises

import (
	"fmt"
	"testing"
)

func TestParallelSum(t *testing.T) {
	nums := make([]int, 1000)
	for i := range nums {
		nums[i] = i + 1
	}
	for _, parts := range []int{1, 4, 7, 2000} {
		t.Run(fmt.Sprintf("parts=%d", parts), func(t *testing.T) {
			if got := ParallelSum(nums, parts); got != 500500 {
				t.Errorf("ParallelSum() = %d, muốn 500500", got)
			}
		})
	}
	if got := ParallelSum(nil, 3); got != 0 {
		t.Errorf("ParallelSum(nil) = %d, muốn 0", got)
	}
}
