// Package hub fans out messages to websocket clients by topic.
//
// Topics are strings like "markets", "book:BTC" or "trades:BTC". Each message is encoded
// once and shared by every subscriber. A client that can't keep up is disconnected, not
// waited for, so one slow browser never delays everyone else.
package hub

import (
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
)

// MaxTopicsPerClient bounds how many topics one connection can subscribe to.
const MaxTopicsPerClient = 16

var (
	ErrUnknownTopic   = errors.New("unknown topic")
	ErrTooManyTopics  = errors.New("too many subscriptions")
	ErrClientIsClosed = errors.New("client closed")
)

// SnapshotFunc returns the current state of topic, sent to a client when it subscribes.
// ok is false if the topic doesn't exist. A nil snapshot means there's nothing to send yet.
type SnapshotFunc func(topic string) (snapshot any, ok bool)

// Message is the wire format sent to clients.
type Message struct {
	Topic string `json:"topic"`
	// Snapshot marks the first message after subscribing, which replaces client state
	// instead of updating it.
	Snapshot bool `json:"snapshot,omitempty"`
	Data     any  `json:"data"`
}

type Hub struct {
	snapshot SnapshotFunc

	mu     sync.Mutex
	topics map[string]map[*Client]struct{}
}

func New(snapshot SnapshotFunc) *Hub {
	return &Hub{snapshot: snapshot, topics: map[string]map[*Client]struct{}{}}
}

// Client is one websocket connection's subscription state. Read outgoing messages from
// Send. Done is closed when the hub drops the client (e.g. it fell behind).
type Client struct {
	Send chan []byte
	Done chan struct{}

	topics map[string]struct{}
	closed bool
}

// NewClient registers a client with a buffer of size messages.
func (h *Hub) NewClient(size int) *Client {
	return &Client{
		Send:   make(chan []byte, size),
		Done:   make(chan struct{}),
		topics: map[string]struct{}{},
	}
}

// Subscribe adds c to topic and queues the topic's snapshot as c's next message. Holding
// the hub lock throughout means no update can be queued ahead of the snapshot.
func (h *Hub) Subscribe(c *Client, topic string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c.closed {
		return ErrClientIsClosed
	}
	if _, ok := c.topics[topic]; ok {
		return nil
	}
	if len(c.topics) >= MaxTopicsPerClient {
		return ErrTooManyTopics
	}
	snap, ok := h.snapshot(topic)
	if !ok {
		return ErrUnknownTopic
	}
	if snap != nil {
		buf, err := json.Marshal(Message{Topic: topic, Snapshot: true, Data: snap})
		if err != nil {
			return err
		}
		h.enqueue(c, buf)
	}
	if h.topics[topic] == nil {
		h.topics[topic] = map[*Client]struct{}{}
	}
	h.topics[topic][c] = struct{}{}
	c.topics[topic] = struct{}{}
	return nil
}

func (h *Hub) Unsubscribe(c *Client, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.remove(c, topic)
}

// Remove unsubscribes c from everything. Call it when the connection ends.
func (h *Hub) Remove(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.drop(c)
}

// Publish sends data to every subscriber of topic.
func (h *Hub) Publish(topic string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	subs := h.topics[topic]
	if len(subs) == 0 {
		return
	}
	buf, err := json.Marshal(Message{Topic: topic, Data: data})
	if err != nil {
		slog.Error("hub: encode message", "topic", topic, "err", err)
		return
	}
	for c := range subs {
		h.enqueue(c, buf)
	}
}

// Subscribers reports how many clients follow topic.
func (h *Hub) Subscribers(topic string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.topics[topic])
}

// enqueue must be called with h.mu held.
func (h *Hub) enqueue(c *Client, buf []byte) {
	select {
	case c.Send <- buf:
	default:
		slog.Warn("hub: dropping slow client", "topics", len(c.topics))
		h.drop(c)
	}
}

// drop must be called with h.mu held.
func (h *Hub) drop(c *Client) {
	if c.closed {
		return
	}
	for topic := range c.topics {
		h.remove(c, topic)
	}
	c.closed = true
	close(c.Done)
}

func (h *Hub) remove(c *Client, topic string) {
	delete(c.topics, topic)
	if subs := h.topics[topic]; subs != nil {
		delete(subs, c)
		if len(subs) == 0 {
			delete(h.topics, topic)
		}
	}
}
