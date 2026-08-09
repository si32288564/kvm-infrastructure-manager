package session

import (
	"errors"
	"fmt"
	"testing"
)

func TestBulkCannotConsumeReservedPriorityCapacity(t *testing.T) {
	queue := newTestQueue(t)
	for index := 0; index < 6; index++ {
		err := queue.Enqueue(testEnvelope(StreamInventory, fmt.Sprintf("inventory-%d", index), index, []byte("bulk")))
		if err != nil {
			t.Fatalf("enqueue bulk %d: %v", index, err)
		}
	}
	if err := queue.Enqueue(testEnvelope(StreamInventory, "inventory-overflow", 7, []byte("bulk"))); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("bulk overflow error = %v", err)
	}
	if err := queue.Enqueue(testEnvelope(StreamHeartbeat, "heartbeat", 1, []byte("health"))); err != nil {
		t.Fatalf("priority capacity was consumed by bulk: %v", err)
	}
	dequeued, ok := queue.Dequeue()
	if !ok || dequeued.Stream != StreamHeartbeat {
		t.Fatalf("first dequeue = %#v, %v", dequeued, ok)
	}
}

func TestAuthorityStreamsRoundRobin(t *testing.T) {
	queue := newTestQueue(t)
	for index := 0; index < 2; index++ {
		if err := queue.Enqueue(testEnvelope(StreamCommand, fmt.Sprintf("command-%d", index), index, []byte("command"))); err != nil {
			t.Fatal(err)
		}
	}
	if err := queue.Enqueue(testEnvelope(StreamResult, "result", 1, []byte("result"))); err != nil {
		t.Fatal(err)
	}
	first, _ := queue.Dequeue()
	second, _ := queue.Dequeue()
	if first.Stream != StreamCommand || second.Stream != StreamResult {
		t.Fatalf("round robin streams = %s, %s", first.Stream, second.Stream)
	}
}

func TestEnvelopeRejectsDigestConflictAndOversize(t *testing.T) {
	envelope := testEnvelope(StreamResult, "result", 1, []byte("result"))
	if err := envelope.Validate(64); err != nil {
		t.Fatal(err)
	}
	envelope.PayloadDigest = digestOf([]byte("different"))
	if err := envelope.Validate(64); err == nil {
		t.Fatal("Validate accepted a digest conflict")
	}
	envelope = testEnvelope(StreamResult, "large", 2, []byte("result"))
	if err := envelope.Validate(2); err == nil {
		t.Fatal("Validate accepted an oversized payload")
	}
}

func newTestQueue(t *testing.T) *PriorityQueue {
	t.Helper()
	perStream := make(map[Stream]int)
	for stream := range knownStreams {
		perStream[stream] = 8
	}
	queue, err := NewPriorityQueue(QueueLimits{
		MaxMessageBytes:       64,
		MaxTotalMessages:      8,
		MaxTotalBytes:         64,
		ReservedPriorityMsgs:  2,
		ReservedPriorityBytes: 16,
		PerStreamMessages:     perStream,
	})
	if err != nil {
		t.Fatal(err)
	}
	return queue
}

func testEnvelope(stream Stream, messageID string, sequence int, payload []byte) Envelope {
	return NewEnvelope("host-1", 1, stream, messageID, "v1", string(stream), uint64(sequence), payload)
}

func digestOf(payload []byte) string {
	return NewEnvelope("host", 1, StreamControl, "message", "v1", "scope", 1, payload).PayloadDigest
}
