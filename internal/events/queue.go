package events

import (
	"sync/atomic"

	"poolwatch/internal/domain"
)

type Queue struct {
	ch      chan domain.Event
	dropped atomic.Uint64
}

func New(size int) *Queue {
	return &Queue{ch: make(chan domain.Event, size)}
}

func (q *Queue) Publish(event domain.Event) {
	select {
	case q.ch <- event:
	default:
		q.dropped.Add(1)
	}
}

func (q *Queue) Read() <-chan domain.Event {
	return q.ch
}

func (q *Queue) Len() int {
	return len(q.ch)
}

func (q *Queue) Dropped() uint64 {
	return q.dropped.Load()
}
