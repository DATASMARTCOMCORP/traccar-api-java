package ingest

import (
	"container/heap"
	"sync"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// job is a decoded position on its way through processing. done runs when processing
// ends (Java's EventHandled), which releases a delayed acknowledgement.
type job struct {
	pos  *model.Position
	conn *Conn
	done func()
}

// submit is ProcessingHandler.channelRead: the device stays cached while its position is
// in flight, and the position goes through buffering, then its device's queue. Without
// buffering, Java processes the position right away on the connection's event loop
// thread, before that connection's next message is read; so does submit, unless another
// position of the device is being processed (then it waits its turn in the queue).
func (p *Processor) submit(c *Conn, pos *model.Position, done func()) {
	p.work.Add(1)
	if err := p.cache.addDevice(p.ctx, pos.DeviceID, pos); err != nil {
		p.log.Warn("Cache add device failed", "deviceId", pos.DeviceID, "error", err)
		p.work.Done()
		done()
		return
	}
	j := job{pos: pos, conn: c, done: done}
	if p.bufferingThreshold > 0 {
		p.buffer.accept(j)
	} else {
		p.release(j, true)
	}
}

// release is ProcessingHandler.onReleased: positions of one device are processed one at
// a time, in release order. The first one is processed by the caller when inline is set;
// positions queued behind it are processed by another goroutine, as Java hands them to
// their own connections' threads.
func (p *Processor) release(j job, inline bool) {
	id := j.pos.DeviceID
	p.qmu.Lock()
	q := p.queues[id]
	p.queues[id] = append(q, j)
	p.qmu.Unlock()
	if len(q) > 0 {
		return
	}
	if !inline {
		go p.drain(id)
		return
	}
	p.process(j)
	if p.next(id) {
		go p.drain(id)
	}
}

// next removes the processed head of the device's queue and reports whether another
// position waits.
func (p *Processor) next(deviceID int64) bool {
	p.qmu.Lock()
	defer p.qmu.Unlock()
	q := p.queues[deviceID][1:]
	if len(q) == 0 {
		delete(p.queues, deviceID)
		return false
	}
	p.queues[deviceID] = q
	return true
}

func (p *Processor) drain(deviceID int64) {
	for {
		p.qmu.Lock()
		j := p.queues[deviceID][0]
		p.qmu.Unlock()
		p.process(j)
		if !p.next(deviceID) {
			return
		}
	}
}

// buffering is BufferingManager: with server.buffering.threshold > 0, each device's
// positions wait that long (restarted for later fixes whenever an earlier one arrives)
// and are released in fix time order. Java's timer releases due positions in creation
// order of their timeouts, which is also deadline order; one goroutine does the same
// here.
type buffering struct {
	p    *Processor
	mu   sync.Mutex
	sets map[int64][]*holder // per device, ordered by compareHolders
	due  timerHeap
	seq  uint64
	wake chan struct{}
}

type holder struct {
	j     job
	timer *bufferTimer
}

type bufferTimer struct {
	deadline time.Time
	seq      uint64
	h        *holder
	canceled bool
}

func newBuffering(p *Processor) *buffering {
	b := &buffering{p: p, sets: map[int64][]*holder{}, wake: make(chan struct{}, 1)}
	if p.bufferingThreshold > 0 {
		go b.run()
	}
	return b
}

// compareHolders is Holder.compareTo: fix time, then device time (a missing time compares
// equal), then server time.
func compareHolders(a, b *model.Position) int {
	if c := compareTimes(a.FixTime, b.FixTime); c != 0 {
		return c
	}
	if c := compareTimes(a.DeviceTime, b.DeviceTime); c != 0 {
		return c
	}
	return a.ServerTime.Time.Compare(b.ServerTime.Time)
}

func compareTimes(a, b model.Time) int {
	if a.IsZero() || b.IsZero() {
		return 0
	}
	return a.Time.Compare(b.Time)
}

func (b *buffering) schedule(h *holder) {
	b.seq++
	h.timer = &bufferTimer{deadline: time.Now().Add(b.p.bufferingThreshold), seq: b.seq, h: h}
	heap.Push(&b.due, h.timer)
}

// accept is BufferingManager.accept. A position equal to a buffered one (same times) is
// not added to the set, as in Java's TreeSet, but is still released by its timer.
func (b *buffering) accept(j job) {
	b.mu.Lock()
	h := &holder{j: j}
	b.schedule(h)
	set := b.sets[j.pos.DeviceID]
	i, found := 0, false
	for ; i < len(set); i++ {
		c := compareHolders(set[i].j.pos, j.pos)
		if c == 0 {
			found = true
		}
		if c >= 0 {
			break
		}
	}
	if !found {
		set = append(set[:i], append([]*holder{h}, set[i:]...)...)
		i++
	} else {
		i++ // tailSet(holder, false) starts after the equal element
	}
	for _, later := range set[i:] {
		later.timer.canceled = true
		b.schedule(later)
	}
	b.sets[j.pos.DeviceID] = set
	b.mu.Unlock()
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *buffering) run() {
	timer := time.NewTimer(time.Hour)
	for {
		b.mu.Lock()
		var released []job
		now := time.Now()
		for b.due.Len() > 0 && !b.due[0].deadline.After(now) {
			released = append(released, b.take(heap.Pop(&b.due).(*bufferTimer))...)
		}
		wait := time.Hour
		if b.due.Len() > 0 {
			wait = b.due[0].deadline.Sub(now)
		}
		b.mu.Unlock()
		for _, j := range released {
			b.p.release(j, false)
		}
		timer.Reset(wait)
		select {
		case <-timer.C:
		case <-b.wake:
			if !timer.Stop() {
				<-timer.C
			}
		case <-b.p.ctx.Done():
			return
		}
	}
}

// take handles a fired timer: the set loses the first holder equal to it (Java's
// TreeSet.remove), and the timer's holder is released.
func (b *buffering) take(t *bufferTimer) []job {
	if t.canceled {
		return nil
	}
	id := t.h.j.pos.DeviceID
	set := b.sets[id]
	for i, h := range set {
		if compareHolders(h.j.pos, t.h.j.pos) == 0 {
			set = append(set[:i:i], set[i+1:]...)
			break
		}
	}
	if len(set) == 0 {
		delete(b.sets, id)
	} else {
		b.sets[id] = set
	}
	return []job{t.h.j}
}

// flush releases everything still buffered, in deadline order (at shutdown).
func (b *buffering) flush() {
	b.mu.Lock()
	var released []job
	for b.due.Len() > 0 {
		released = append(released, b.take(heap.Pop(&b.due).(*bufferTimer))...)
	}
	b.mu.Unlock()
	for _, j := range released {
		b.p.release(j, false)
	}
}

type timerHeap []*bufferTimer

func (h timerHeap) Len() int { return len(h) }
func (h timerHeap) Less(i, j int) bool {
	if !h[i].deadline.Equal(h[j].deadline) {
		return h[i].deadline.Before(h[j].deadline)
	}
	return h[i].seq < h[j].seq
}
func (h timerHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *timerHeap) Push(x any)   { *h = append(*h, x.(*bufferTimer)) }
func (h *timerHeap) Pop() any {
	old := *h
	t := old[len(old)-1]
	*h = old[:len(old)-1]
	return t
}
