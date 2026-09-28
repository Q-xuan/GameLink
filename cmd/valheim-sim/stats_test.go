package main

import (
	"testing"
	"time"
)

func TestComputeCounters(t *testing.T) {
	rtts := make([]time.Duration, 100)
	for i := range rtts {
		rtts[i] = time.Duration(i+1) * time.Millisecond
	}
	rep := Compute(100, 100, 100*64, rtts, time.Second)
	if rep.Loss != 0 {
		t.Fatal(rep.Loss)
	}
	if rep.RTTAvg != time.Duration(50.5*float64(time.Millisecond)) {
		t.Fatalf("avg %s", rep.RTTAvg)
	}
	if rep.RTTP95 != 95*time.Millisecond || rep.RTTP99 != 99*time.Millisecond {
		t.Fatalf("p95 %s p99 %s", rep.RTTP95, rep.RTTP99)
	}
	if rep.Throughput != float64(100*64*8) {
		t.Fatal(rep.Throughput)
	}
	loss := Compute(10, 9, 8000, nil, time.Second)
	if loss.Loss != 0.1 || loss.Throughput != 64000 || loss.Jitter != 0 {
		t.Fatalf("%+v", loss)
	}
	j := jitterOf([]time.Duration{10 * time.Millisecond, 20 * time.Millisecond})
	if j != time.Duration(float64(10*time.Millisecond)/16) {
		t.Fatalf("jitter %s", j)
	}
}

func TestPacketCodec(t *testing.T) {
	raw, err := Encode(Packet{SessionID: 7, Sequence: 3, Timestamp: 99, AckSeq: 3, Kind: KindAck}, MinSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != MinSize {
		t.Fatal(len(raw))
	}
	got, err := Decode(raw)
	if err != nil || got.SessionID != 7 || got.Sequence != 3 || got.Timestamp != 99 || got.Kind != KindAck {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := Encode(Packet{Kind: KindData}, 63); err == nil {
		t.Fatal("small size accepted")
	}
	if _, err := Encode(Packet{Kind: KindData}, 1201); err == nil {
		t.Fatal("large size accepted")
	}
}
