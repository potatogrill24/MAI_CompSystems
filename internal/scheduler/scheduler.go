// Package scheduler implements randomized priority search with a serial schedule
// generation scheme for non-preemptive, resource-constrained project scheduling.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"runtime"
	"sort"
	"sync"

	"mai/compsystems/internal/model"
)

const MaxResource = 10
const DefaultIterations = 1_000_000
const MaxWorks = 100
const MaxDuration = 1_000_000

type Slot struct {
	WorkID   int `json:"work_id"`
	Start    int `json:"start"`
	Finish   int `json:"finish"`
	Resource int `json:"resource"`
}
type Result struct {
	Duration   int    `json:"duration"`
	Iterations int    `json:"iterations"`
	Workers    int    `json:"workers"`
	Seed       uint64 `json:"seed"`
	Sequence   []int  `json:"sequence"`
	Schedule   []Slot `json:"schedule"`
}
type Request struct {
	Works      []model.Work `json:"works"`
	Iterations int          `json:"iterations,omitempty"`
	Workers    int          `json:"workers,omitempty"`
	Seed       uint64       `json:"seed,omitempty"`
}

func Validate(works []model.Work) error {
	if len(works) > MaxWorks {
		return fmt.Errorf("at most %d works allowed", MaxWorks)
	}
	ids := make(map[int]int, len(works))
	for i, w := range works {
		if w.ID <= 0 {
			return errors.New("work id must be positive")
		}
		if _, ok := ids[w.ID]; ok {
			return errors.New("duplicate work id")
		}
		ids[w.ID] = i
		if w.Duration <= 0 || w.Duration > MaxDuration {
			return fmt.Errorf("duration must be in [1,%d]", MaxDuration)
		}
		if w.Resource <= 0 || w.Resource > MaxResource {
			return errors.New("resource must be in [1,10]")
		}
		if w.Task == "" {
			return errors.New("task is required")
		}
	}
	indegree := make([]int, len(works))
	next := make([][]int, len(works))
	for i, w := range works {
		seen := map[int]bool{}
		for _, p := range w.Pred {
			j, ok := ids[p]
			if !ok {
				return fmt.Errorf("predecessor %d does not exist in this project", p)
			}
			if seen[p] || p == w.ID {
				return errors.New("duplicate or self predecessor")
			}
			seen[p] = true
			indegree[i]++
			next[j] = append(next[j], i)
		}
	}
	q := []int{}
	for i, d := range indegree {
		if d == 0 {
			q = append(q, i)
		}
	}
	for n := 0; n < len(q); n++ {
		for _, j := range next[q[n]] {
			indegree[j]--
			if indegree[j] == 0 {
				q = append(q, j)
			}
		}
	}
	if len(q) != len(works) {
		return errors.New("predecessors contain a cycle")
	}
	return nil
}

// candidate randomly chooses a ready work, then inserts it at the earliest
// feasible time. Starts/finishes are event boundaries, so long durations do not
// allocate a time-indexed resource array.
func candidate(works []model.Work, preds [][]int, rng *rand.Rand) Result {
	n := len(works)
	done := make([]bool, n)
	finish := make([]int, n)
	slots := make([]Slot, 0, n)
	sequence := make([]int, 0, n)
	ready := make([]int, 0, n)
	events := make([]int, 0, n+1)
	duration := 0
	for len(slots) < n {
		ready = ready[:0]
		for i := range works {
			if done[i] {
				continue
			}
			ok := true
			for _, p := range preds[i] {
				if !done[p] {
					ok = false
					break
				}
			}
			if ok {
				ready = append(ready, i)
			}
		}
		i := ready[rng.IntN(len(ready))]
		w := works[i]
		earliest := 0
		for _, p := range preds[i] {
			earliest = max(earliest, finish[p])
		}
		events = append(events[:0], earliest)
		for _, s := range slots {
			if s.Finish > earliest {
				events = append(events, s.Finish)
			}
		}
		sort.Ints(events)
		start := earliest
		for _, t := range events {
			feasible := true
			// Resource usage can only increase at t or at an existing start.
			for k := -1; k < len(slots); k++ {
				at := t
				if k >= 0 {
					at = slots[k].Start
				}
				if at < t || at >= t+w.Duration {
					continue
				}
				used := w.Resource
				for _, s := range slots {
					if s.Start <= at && at < s.Finish {
						used += s.Resource
					}
				}
				if used > MaxResource {
					feasible = false
					break
				}
			}
			if feasible {
				start = t
				break
			}
		}
		finish[i] = start + w.Duration
		done[i] = true
		duration = max(duration, finish[i])
		sequence = append(sequence, w.ID)
		slots = append(slots, Slot{w.ID, start, finish[i], w.Resource})
	}
	return Result{Duration: duration, Sequence: sequence, Schedule: slots}
}

func Calculate(ctx context.Context, req Request) (Result, error) {
	if err := Validate(req.Works); err != nil {
		return Result{}, err
	}
	if req.Iterations == 0 {
		req.Iterations = DefaultIterations
	}
	if req.Iterations < 1 || req.Iterations > DefaultIterations {
		return Result{}, errors.New("iterations must be in [1,1000000]")
	}
	if req.Workers == 0 {
		req.Workers = min(runtime.GOMAXPROCS(0), 32)
	}
	if req.Workers < 1 || req.Workers > 64 {
		return Result{}, errors.New("workers must be in [1,64]")
	}
	req.Workers = min(req.Workers, req.Iterations)
	ids := map[int]int{}
	for i, w := range req.Works {
		ids[w.ID] = i
	}
	preds := make([][]int, len(req.Works))
	for i, w := range req.Works {
		for _, p := range w.Pred {
			preds[i] = append(preds[i], ids[p])
		}
	}
	type winner struct {
		result    Result
		iteration int
	}
	ch := make(chan winner, req.Workers)
	var wg sync.WaitGroup
	for worker := 0; worker < req.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			best := winner{iteration: -1}
			for i := worker; i < req.Iterations; i += req.Workers {
				if ctx.Err() != nil {
					return
				}
				// Per-iteration seeds make results independent of worker count/scheduling.
				rng := rand.New(rand.NewPCG(req.Seed, uint64(i)))
				r := candidate(req.Works, preds, rng)
				if best.iteration < 0 || r.Duration < best.result.Duration {
					best = winner{r, i}
				}
			}
			ch <- best
		}(worker)
	}
	wg.Wait()
	close(ch)
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	best := winner{iteration: -1}
	for b := range ch {
		if best.iteration < 0 || b.result.Duration < best.result.Duration || (b.result.Duration == best.result.Duration && b.iteration < best.iteration) {
			best = b
		}
	}
	best.result.Iterations = req.Iterations
	best.result.Workers = req.Workers
	best.result.Seed = req.Seed
	return best.result, nil
}
