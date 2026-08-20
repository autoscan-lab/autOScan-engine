package engine

import (
	"runtime"
	"sync"
)

// fn must only write per-index storage and read otherwise-immutable shared data.
func parallelForEach(n int, fn func(i int)) {
	if n <= 0 {
		return
	}

	workers := runtime.NumCPU()
	if workers > n {
		workers = n
	}
	if workers < 1 {
		workers = 1
	}

	var wg sync.WaitGroup
	wg.Add(workers)
	ch := make(chan int)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := range ch {
				fn(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		ch <- i
	}
	close(ch)
	wg.Wait()
}
