package scheduler

import (
	"context"
	"errors"
	"mai/compsystems/internal/model"
	"reflect"
	"testing"
)

func work(id, duration, resource int, pred ...int) model.Work {
	return model.Work{ID: id, Task: "work", Duration: duration, Resource: resource, Pred: pred}
}
func verify(t *testing.T, works []model.Work, result Result) {
	t.Helper()
	if len(result.Schedule) != len(works) {
		t.Fatal("missing work")
	}
	slots := map[int]Slot{}
	for _, s := range result.Schedule {
		if _, ok := slots[s.WorkID]; ok {
			t.Fatal("duplicate slot")
		}
		slots[s.WorkID] = s
	}
	end := 0
	for _, w := range works {
		s, ok := slots[w.ID]
		if !ok || s.Finish-s.Start != w.Duration || s.Start < 0 {
			t.Fatalf("invalid slot %+v", s)
		}
		end = max(end, s.Finish)
		for _, p := range w.Pred {
			if slots[p].Finish > s.Start {
				t.Fatal("predecessor violation")
			}
		}
	}
	for _, s := range result.Schedule {
		used := 0
		for _, v := range result.Schedule {
			if v.Start <= s.Start && s.Start < v.Finish {
				used += v.Resource
			}
		}
		if used > MaxResource {
			t.Fatalf("resource usage %d", used)
		}
	}
	if end != result.Duration {
		t.Fatal("wrong makespan")
	}
}
func TestSchedules(t *testing.T) {
	for _, tc := range []struct {
		name     string
		works    []model.Work
		duration int
	}{
		{"parallel", []model.Work{work(1, 5, 4), work(2, 3, 6)}, 5},
		{"resource serial", []model.Work{work(1, 5, 6), work(2, 3, 6)}, 8},
		{"chain", []model.Work{work(1, 5, 1), work(2, 3, 1, 1)}, 8},
		{"diamond", []model.Work{work(1, 2, 10), work(2, 3, 4, 1), work(3, 5, 6, 1), work(4, 1, 10, 2, 3)}, 8},
		{"empty", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Calculate(context.Background(), Request{Works: tc.works, Iterations: 1000, Workers: 4})
			if err != nil {
				t.Fatal(err)
			}
			verify(t, tc.works, r)
			if r.Duration != tc.duration {
				t.Fatalf("got %d want %d", r.Duration, tc.duration)
			}
		})
	}
}
func TestInvalidGraph(t *testing.T) {
	for _, works := range [][]model.Work{
		{work(1, 1, 11)}, {work(1, 0, 1)}, {work(1, 1, 1, 2)}, {work(1, 1, 1, 1)}, {work(1, 1, 1), work(1, 1, 1)}, {work(1, 1, 1, 2), work(2, 1, 1, 1)}, {work(1, 1, 1), work(2, 1, 1, 1, 1)},
	} {
		if err := Validate(works); err == nil {
			t.Fatalf("accepted %+v", works)
		}
	}
}
func TestDeterministicWorkers(t *testing.T) {
	works := []model.Work{work(1, 8, 4), work(2, 3, 6), work(3, 7, 7), work(4, 2, 3, 1), work(5, 5, 5, 2)}
	a, err := Calculate(context.Background(), Request{Works: works, Iterations: 500, Workers: 1, Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Calculate(context.Background(), Request{Works: works, Iterations: 500, Workers: 7, Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	a.Workers = b.Workers
	if !reflect.DeepEqual(a, b) {
		t.Fatal("result depends on workers")
	}
	verify(t, works, a)
}
func TestMillionSequences(t *testing.T) {
	if testing.Short() {
		t.Skip("million sequence run")
	}
	works := []model.Work{work(1, 2, 6), work(2, 3, 4), work(3, 4, 7, 1)}
	r, err := Calculate(context.Background(), Request{Works: works, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if r.Iterations != 1_000_000 {
		t.Fatal("wrong count")
	}
	verify(t, works, r)
}
func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Calculate(ctx, Request{Works: []model.Work{work(1, 1, 1)}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestManyGraphs(t *testing.T) {
	for seed := uint64(0); seed < 100; seed++ {
		works := []model.Work{}
		for i := 1; i <= 12; i++ {
			w := work(i, int(seed%7)+i, int((seed+uint64(i))%10)+1)
			if i > 2 && i%2 == 0 {
				w.Pred = []int{i - 2}
			}
			works = append(works, w)
		}
		r, err := Calculate(context.Background(), Request{Works: works, Iterations: 30, Seed: seed})
		if err != nil {
			t.Fatal(err)
		}
		verify(t, works, r)
	}
}
