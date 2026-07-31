package publishing

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMemoryTIDAllocatorResolvesKnownCollisionIdempotently(t *testing.T) {
	allocator := NewMemoryStore()
	ctx := context.Background()
	const repository = "did:plc:collision"
	physical := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC).UnixMicro()
	keys := []string{
		"publish_c89c98f88e44e5bbd531ed13d967f34619c5fc0e6fe4711c7c517f3b2473308f",
		"publish_8292a09311bfe2e4ca21ac76822f570371c77db6c8cb034fe53189d0e473308f",
	}
	first, err := allocator.AllocateMonotonicTID(
		ctx, repository, keys[0], physical,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := allocator.AllocateMonotonicTID(
		ctx, repository, keys[1], physical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second != first+1 {
		t.Fatalf("known collision allocations first=%d second=%d", first, second)
	}
	replayed, err := allocator.AllocateMonotonicTID(
		ctx, repository, keys[0], physical,
	)
	if err != nil || replayed != first {
		t.Fatalf("replayed=%d first=%d error=%v", replayed, first, err)
	}
}

func TestMemoryTIDAllocatorUsesRealServiceKeysAndPhysicalOrder(t *testing.T) {
	allocator := NewMemoryStore()
	ctx := context.Background()
	const repository = "did:plc:servicekeys"
	base := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC).UnixMicro()
	snapshot := strings.Repeat("a", 64)
	var previous uint64
	for index := 0; index < 20_000; index++ {
		key := destinationIdempotencyKey(
			fmt.Sprintf("command-%d", index),
			"channel-bluesky",
			1,
			snapshot,
		)
		value, err := allocator.AllocateMonotonicTID(
			ctx,
			repository,
			key,
			base+int64(index%3),
		)
		if err != nil {
			t.Fatal(err)
		}
		if index > 0 && value <= previous {
			t.Fatalf("allocation %d=%d previous=%d", index, value, previous)
		}
		if int64(value>>10) < base+int64(index%3) {
			t.Fatalf("allocation %d predates physical timestamp", index)
		}
		previous = value
	}
}

func TestMemoryTIDAllocatorCASIsConcurrentAndUnique(t *testing.T) {
	allocator := NewMemoryStore()
	ctx := context.Background()
	const (
		repository = "did:plc:concurrent"
		total      = 256
	)
	physical := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC).UnixMicro()
	values := make(chan uint64, total)
	failures := make(chan error, total)
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := 0; index < total; index++ {
		key := destinationIdempotencyKey(
			fmt.Sprintf("command-concurrent-%d", index),
			"channel-bluesky",
			1,
			strings.Repeat("b", 64),
		)
		group.Add(1)
		go func(key string) {
			defer group.Done()
			<-start
			value, err := allocator.AllocateMonotonicTID(
				ctx, repository, key, physical,
			)
			values <- value
			failures <- err
		}(key)
	}
	close(start)
	group.Wait()
	close(values)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := make(map[uint64]struct{}, total)
	for value := range values {
		if _, duplicate := seen[value]; duplicate {
			t.Fatalf("duplicate concurrent allocation %d", value)
		}
		seen[value] = struct{}{}
	}
	if len(seen) != total {
		t.Fatalf("allocations=%d want=%d", len(seen), total)
	}
}
