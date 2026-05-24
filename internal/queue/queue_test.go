package queue

import (
	"context"
	"testing"
	"time"

	"github.com/0xarchit/contestsync/config"
)

func TestInMemoryQueuePublish(t *testing.T) {
	cfg := &config.Config{
		KafkaHost: "",
	}

	q, err := New(cfg, nil, nil)
	if err != nil {
		t.Fatalf("failed to create queue: %v", err)
	}

	if !q.useInMemory {
		t.Error("expected useInMemory to be true")
	}

	ctx := context.Background()
	err = q.PublishSyncTask(ctx, 42)
	if err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	select {
	case uid := <-q.syncCh:
		if uid != 42 {
			t.Errorf("expected user ID 42, got %d", uid)
		}
	default:
		t.Error("expected task to be queued")
	}
}

func TestQueueDrain(t *testing.T) {
	cfg := &config.Config{
		KafkaHost: "",
	}

	q, err := New(cfg, nil, nil)
	if err != nil {
		t.Fatalf("failed to create queue: %v", err)
	}

	done := make(chan struct{})
	q.wg.Add(1)

	go func() {
		time.Sleep(100 * time.Millisecond)
		q.wg.Done()
		close(done)
	}()

	q.Drain()

	select {
	case <-done:
	default:
		t.Error("expected Drain to block until wg is Done")
	}
}
