package exercises

import "sync"

func WorkerPool[T, R any](jobs []T, workers int, fn func(T) R) []R {
    if len(jobs) == 0 {
        return []R{}
    }

    if workers <= 0 {
        workers = 1
    }

    if workers > len(jobs) {
        workers = len(jobs)
    }

    results := make([]R, len(jobs))

    // Channel chứa index của job
    jobQueue := make(chan int)

    var wg sync.WaitGroup

    // Khởi động workers
    for w := 0; w < workers; w++ {
        wg.Add(1)

        go func() {
            defer wg.Done()

            for idx := range jobQueue {
                results[idx] = fn(jobs[idx])
            }
        }()
    }

    // Đưa job vào channel
    for i := range jobs {
        jobQueue <- i
    }

    // Báo hiệu không còn job
    close(jobQueue)

    // Chờ tất cả worker hoàn thành
    wg.Wait()

    return results
}