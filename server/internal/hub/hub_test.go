package hub

import (
	"encoding/json"
	"errors"
	"testing"
)

func snapshots(topic string) (any, bool) {
	switch topic {
	case "book:BTC":
		return map[string]string{"state": "snap"}, true
	case "trades:BTC":
		return nil, true // exists, nothing yet
	}
	return nil, false
}

func recv(t *testing.T, c *Client) Message {
	t.Helper()
	select {
	case buf := <-c.Send:
		var m Message
		if err := json.Unmarshal(buf, &m); err != nil {
			t.Fatal(err)
		}
		return m
	default:
		t.Fatal("no message queued")
		return Message{}
	}
}

func TestSubscribeSendsSnapshotThenUpdates(t *testing.T) {
	h := New(snapshots)
	c := h.NewClient(8)

	if err := h.Subscribe(c, "book:BTC"); err != nil {
		t.Fatal(err)
	}
	h.Publish("book:BTC", "update")
	h.Publish("book:ETH", "not subscribed")

	if m := recv(t, c); !m.Snapshot || m.Topic != "book:BTC" {
		t.Errorf("first message = %+v, want snapshot", m)
	}
	if m := recv(t, c); m.Snapshot || m.Data != "update" {
		t.Errorf("second message = %+v, want update", m)
	}
	if len(c.Send) != 0 {
		t.Errorf("%d unexpected messages queued", len(c.Send))
	}
}

func TestSubscribeWithoutSnapshot(t *testing.T) {
	h := New(snapshots)
	c := h.NewClient(8)
	if err := h.Subscribe(c, "trades:BTC"); err != nil {
		t.Fatal(err)
	}
	if len(c.Send) != 0 {
		t.Error("queued a nil snapshot")
	}
	if h.Subscribers("trades:BTC") != 1 {
		t.Error("not subscribed")
	}
}

func TestSubscribeErrors(t *testing.T) {
	h := New(func(string) (any, bool) { return nil, true })
	c := h.NewClient(8)
	if err := New(snapshots).Subscribe(c, "book:NOPE"); !errors.Is(err, ErrUnknownTopic) {
		t.Errorf("unknown topic err = %v", err)
	}
	for i := range MaxTopicsPerClient {
		if err := h.Subscribe(c, string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Subscribe(c, "one-too-many"); !errors.Is(err, ErrTooManyTopics) {
		t.Errorf("limit err = %v", err)
	}
	if err := h.Subscribe(c, "a"); err != nil {
		t.Errorf("re-subscribing an existing topic should be a no-op, got %v", err)
	}
}

func TestSlowClientIsDropped(t *testing.T) {
	h := New(snapshots)
	slow := h.NewClient(1)
	fast := h.NewClient(8)
	h.Subscribe(slow, "book:BTC") // fills slow's buffer with the snapshot
	h.Subscribe(fast, "book:BTC")

	h.Publish("book:BTC", "update")

	select {
	case <-slow.Done:
	default:
		t.Fatal("slow client not dropped")
	}
	if h.Subscribers("book:BTC") != 1 {
		t.Errorf("subscribers = %d, want 1", h.Subscribers("book:BTC"))
	}
	if err := h.Subscribe(slow, "book:BTC"); !errors.Is(err, ErrClientIsClosed) {
		t.Errorf("subscribe after drop err = %v", err)
	}
	if len(fast.Send) != 2 {
		t.Errorf("fast client got %d messages, want 2", len(fast.Send))
	}
}

func TestRemoveAndUnsubscribe(t *testing.T) {
	h := New(snapshots)
	c := h.NewClient(8)
	h.Subscribe(c, "book:BTC")
	h.Subscribe(c, "trades:BTC")

	h.Unsubscribe(c, "book:BTC")
	if h.Subscribers("book:BTC") != 0 || h.Subscribers("trades:BTC") != 1 {
		t.Error("unsubscribe removed the wrong topic")
	}
	h.Remove(c)
	h.Remove(c) // idempotent
	if h.Subscribers("trades:BTC") != 0 {
		t.Error("remove left a subscription")
	}
}
