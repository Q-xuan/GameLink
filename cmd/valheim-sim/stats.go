package main

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// Report is the counter summary printed by the simulator.
type Report struct {
	Sent       uint64
	Recv       uint64
	Loss       float64
	RTTAvg     time.Duration
	RTTP95     time.Duration
	RTTP99     time.Duration
	Jitter     time.Duration
	Throughput float64
	Bytes      uint64
}

func (r Report) String() string {
	return fmt.Sprintf("sent=%d recv=%d loss=%.2f%% rtt_avg=%s rtt_p95=%s rtt_p99=%s jitter=%s throughput=%.0fbps",
		r.Sent, r.Recv, r.Loss*100, r.RTTAvg, r.RTTP95, r.RTTP99, r.Jitter, r.Throughput)
}

// Compute summarizes counters. rtts is in arrival order; percentiles sort a copy.
// Loss is (sent-recv)/sent. Throughput is sent payload bits per second.
func Compute(sent, recv, bytes uint64, rtts []time.Duration, elapsed time.Duration) Report {
	rep := Report{Sent: sent, Recv: recv, Bytes: bytes, Jitter: jitterOf(rtts)}
	if sent > 0 && recv < sent {
		rep.Loss = float64(sent-recv) / float64(sent)
	}
	if len(rtts) > 0 {
		var sum float64
		for _, d := range rtts {
			sum += float64(d)
		}
		rep.RTTAvg = time.Duration(sum / float64(len(rtts)))
		sorted := append([]time.Duration(nil), rtts...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		rep.RTTP95 = percentile(sorted, 95)
		rep.RTTP99 = percentile(sorted, 99)
	}
	if elapsed > 0 {
		rep.Throughput = float64(bytes) * 8 / elapsed.Seconds()
	}
	return rep
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

func jitterOf(rtts []time.Duration) time.Duration {
	if len(rtts) < 2 {
		return 0
	}
	var j float64
	prev := rtts[0]
	for _, cur := range rtts[1:] {
		d := cur - prev
		if d < 0 {
			d = -d
		}
		j += (float64(d) - j) / 16
		prev = cur
	}
	return time.Duration(j)
}

// Collector accumulates send and ACK samples from one simulator endpoint.
type Collector struct {
	mu    sync.Mutex
	sent  uint64
	recv  uint64
	bytes uint64
	rtts  []time.Duration
	start time.Time
}

func (c *Collector) Start() {
	c.mu.Lock()
	c.start = time.Now()
	c.mu.Unlock()
}

func (c *Collector) AddSend(n int) {
	c.mu.Lock()
	c.sent++
	c.bytes += uint64(n)
	c.mu.Unlock()
}

func (c *Collector) AddRecv(rtt time.Duration) {
	c.mu.Lock()
	c.recv++
	c.rtts = append(c.rtts, rtt)
	c.mu.Unlock()
}

func (c *Collector) RecvCount() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recv
}

func (c *Collector) Finish() Report {
	c.mu.Lock()
	defer c.mu.Unlock()
	elapsed := time.Since(c.start)
	return Compute(c.sent, c.recv, c.bytes, append([]time.Duration(nil), c.rtts...), elapsed)
}
