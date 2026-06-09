package engine

import (
	"runtime"
	"sync"
)

// parallelForEach runs fn(i) for each i in [0, n) using up to NumCPU workers.
// fn must be safe to run concurrently: it should only write to per-index storage
// (e.g. slots[i]) and read otherwise-immutable shared data, so there is no race.
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
