package session

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrQueueFull       = errors.New("agent session queue is full")
	ErrMessageTooLarge = errors.New("agent session message is too large")
)

// QueueLimits reserve capacity so bulk streams cannot consume authority and
// health traffic capacity. Limits are local safety bounds, not authority.
type QueueLimits struct {
	MaxMessageBytes       int
	MaxTotalMessages      int
	MaxTotalBytes         int
	ReservedPriorityMsgs  int
	ReservedPriorityBytes int
	PerStreamMessages     map[Stream]int
}

// PriorityQueue is a bounded application-level scheduler shared by one Agent
// transport session. FIFO is guaranteed only inside one logical stream.
type PriorityQueue struct {
	mu              sync.Mutex
	limits          QueueLimits
	lanes           map[Stream][]Envelope
	totalCount      int
	totalBytes      int
	authorityCursor int
	bulkCursor      int
}

var (
	controlStreams   = []Stream{StreamControl}
	authorityStreams = []Stream{StreamCommand, StreamResult, StreamHeartbeat, StreamCredential}
	bulkStreams      = []Stream{StreamInventory, StreamResync}
)

// NewPriorityQueue validates and constructs a bounded scheduler.
func NewPriorityQueue(limits QueueLimits) (*PriorityQueue, error) {
	if limits.MaxMessageBytes < 1 || limits.MaxTotalMessages < 1 || limits.MaxTotalBytes < 1 {
		return nil, errors.New("positive message, count, and byte limits are required")
	}
	if limits.ReservedPriorityMsgs < 0 || limits.ReservedPriorityMsgs >= limits.MaxTotalMessages {
		return nil, errors.New("reserved priority message capacity is invalid")
	}
	if limits.ReservedPriorityBytes < 0 || limits.ReservedPriorityBytes >= limits.MaxTotalBytes {
		return nil, errors.New("reserved priority byte capacity is invalid")
	}
	for stream := range knownStreams {
		if limits.PerStreamMessages[stream] < 1 {
			return nil, fmt.Errorf("positive queue limit is required for stream %s", stream)
		}
	}
	return &PriorityQueue{limits: limits, lanes: make(map[Stream][]Envelope)}, nil
}

// Enqueue adds one validated envelope without blocking or silent dropping.
func (queue *PriorityQueue) Enqueue(envelope Envelope) error {
	if len(envelope.Payload) > queue.limits.MaxMessageBytes {
		return ErrMessageTooLarge
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()

	lane := queue.lanes[envelope.Stream]
	if len(lane) >= queue.limits.PerStreamMessages[envelope.Stream] {
		return ErrQueueFull
	}
	if queue.totalCount+1 > queue.limits.MaxTotalMessages || queue.totalBytes+len(envelope.Payload) > queue.limits.MaxTotalBytes {
		return ErrQueueFull
	}
	if isBulk(envelope.Stream) {
		if queue.totalCount+1 > queue.limits.MaxTotalMessages-queue.limits.ReservedPriorityMsgs ||
			queue.totalBytes+len(envelope.Payload) > queue.limits.MaxTotalBytes-queue.limits.ReservedPriorityBytes {
			return ErrQueueFull
		}
	}
	queue.lanes[envelope.Stream] = append(lane, envelope)
	queue.totalCount++
	queue.totalBytes += len(envelope.Payload)
	return nil
}

// Dequeue returns Control first, then round-robins authority/health streams,
// then round-robins bulk streams. No global arrival ordering is implied.
func (queue *PriorityQueue) Dequeue() (Envelope, bool) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if envelope, ok := queue.popFirst(controlStreams, nil); ok {
		return envelope, true
	}
	if envelope, ok := queue.popFirst(authorityStreams, &queue.authorityCursor); ok {
		return envelope, true
	}
	return queue.popFirst(bulkStreams, &queue.bulkCursor)
}

// Stats returns bounded in-memory queue use.
func (queue *PriorityQueue) Stats() (messages, bytes int) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return queue.totalCount, queue.totalBytes
}

func (queue *PriorityQueue) popFirst(streams []Stream, cursor *int) (Envelope, bool) {
	start := 0
	if cursor != nil {
		start = *cursor % len(streams)
	}
	for offset := range streams {
		index := (start + offset) % len(streams)
		stream := streams[index]
		lane := queue.lanes[stream]
		if len(lane) == 0 {
			continue
		}
		envelope := lane[0]
		queue.lanes[stream] = lane[1:]
		queue.totalCount--
		queue.totalBytes -= len(envelope.Payload)
		if cursor != nil {
			*cursor = (index + 1) % len(streams)
		}
		return envelope, true
	}
	return Envelope{}, false
}

func isBulk(stream Stream) bool {
	return stream == StreamInventory || stream == StreamResync
}
