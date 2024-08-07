package goring

import (
	"sync"
	"sync/atomic"
)

type ringBuffer[T any] struct {
	buffer []T
	head   int64
	tail   int64
	mod    int64
}

type Queue[T any] struct {
	len         int64
	initialSize int64
	content     *ringBuffer[T]
	lock        sync.Mutex
}

func New[T any](initialSize int64) *Queue[T] {
	return &Queue[T]{
		content: &ringBuffer[T]{
			buffer: make([]T, initialSize),
			head:   0,
			tail:   0,
			mod:    initialSize,
		},
		initialSize: initialSize,
		len:         0,
	}
}

func (q *Queue[T]) Push(item T) {
	q.lock.Lock()
	c := q.content
	c.tail = (c.tail + 1) % c.mod
	if c.tail == c.head {
		q.grow(2)
	}
	atomic.AddInt64(&q.len, 1)
	q.content.buffer[q.content.tail] = item
	q.lock.Unlock()
}

func (q *Queue[T]) Length() int64 {
	return atomic.LoadInt64(&q.len)
}

func (q *Queue[T]) Empty() bool {
	return q.Length() == 0
}

// single consumer
func (q *Queue[T]) Pop() (T, bool) {
	var t T
	if q.Empty() {
		return t, false
	}
	// as we are a single consumer, no other thread can have poped the items there are guaranteed to be items now

	q.lock.Lock()
	c := q.content
	c.head = (c.head + 1) % c.mod
	res := c.buffer[c.head]
	c.buffer[c.head] = t
	atomic.AddInt64(&q.len, -1)
	//q.tryResize()
	q.lock.Unlock()
	return res, true
}

func (q *Queue[T]) PopMany(count int64) ([]T, bool) {
	if q.Empty() {
		return nil, false
	}

	var t T
	q.lock.Lock()
	c := q.content

	if count >= q.len {
		count = q.len
	}
	atomic.AddInt64(&q.len, -count)

	buffer := make([]T, count)
	for i := int64(0); i < count; i++ {
		pos := (c.head + 1 + i) % c.mod
		buffer[i] = c.buffer[pos]
		c.buffer[pos] = t
	}
	c.head = (c.head + count) % c.mod
	//q.tryResize()
	q.lock.Unlock()
	return buffer, true
}

func (q *Queue[T]) grow(fillFactor int64) {
	c := q.content
	//var fillFactor int64 = 2
	// we need to grow

	newLen := c.mod * fillFactor
	newBuff := make([]T, newLen)

	for i := int64(0); i < c.mod; i++ {
		buffIndex := (c.tail + i) % c.mod
		newBuff[i] = c.buffer[buffIndex]
	}
	// set the new buffer and reset head and tail
	newContent := &ringBuffer[T]{
		buffer: newBuff,
		head:   0,
		tail:   c.mod,
		mod:    newLen,
	}
	q.content = newContent
}

//func (q *Queue[T]) tryResize() {
//	if !q.Empty() {
//		return
//	}
//
//	c := q.content
//	if c.mod < q.initialSize*5 {
//		return
//	}
//
//	mod := c.mod / 2
//	if mod < q.initialSize {
//		mod = q.initialSize
//	}
//
//	newBuff := c.buffer[:mod]
//	newContent := &ringBuffer[T]{
//		buffer: newBuff,
//		head:   0,
//		tail:   mod,
//		mod:    mod,
//	}
//	q.content = newContent
//}
