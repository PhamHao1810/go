package exercises

import "sync"

func ParallelSum(nums []int, parts int) int {
    if len(nums) == 0 || parts <= 0 {
        return 0
    }

    if parts > len(nums) {
        parts = len(nums)
    }

    results := make([]int, parts)

    var wg sync.WaitGroup

    for i := 0; i < parts; i++ {
        start := i * len(nums) / parts
        end := (i + 1) * len(nums) / parts

        wg.Add(1)

        go func(idx, start, end int) {
            defer wg.Done()

            sum := 0

            for j := start; j < end; j++ {
                sum += nums[j]
            }

            results[idx] = sum
        }(i, start, end)
    }

    wg.Wait()

    total := 0

    for _, sum := range results {
        total += sum
    }

    return total
}