package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/juancruzfl/paperperps/server/internal/hub"
)

func TestWebsocketSubscribeFlow(t *testing.T) {
	h := hub.New(func(topic string) (any, bool) {
		if topic == "book:BTC" {
			return map[string]string{"coin": "BTC"}, true
		}
		return nil, false
	})
	srv := httptest.NewServer(handleWS(h, func(context.Context, string) (uuid.UUID, error) {
		return uuid.Nil, errors.New("no accounts in this test")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	send := func(op, topic string) {
		buf, _ := json.Marshal(clientRequest{Op: op, Topic: topic})
		if err := conn.Write(ctx, websocket.MessageText, buf); err != nil {
			t.Fatal(err)
		}
	}
	read := func() map[string]any {
		_, buf, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		json.Unmarshal(buf, &m)
		return m
	}

	send("subscribe", "book:BTC")
	if m := read(); m["topic"] != "book:BTC" || m["snapshot"] != true {
		t.Errorf("first message = %v, want book snapshot", m)
	}

	for h.Subscribers("book:BTC") == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	h.Publish("book:BTC", "update")
	if m := read(); m["data"] != "update" || m["snapshot"] != nil {
		t.Errorf("second message = %v, want update", m)
	}

	send("subscribe", "book:NOPE")
	if m := read(); m["topic"] != "error" || !strings.Contains(m["data"].(map[string]any)["error"].(string), "unknown topic") {
		t.Errorf("error reply = %v", m)
	}

	send("unsubscribe", "book:BTC")
	deadline := time.Now().Add(2 * time.Second)
	for h.Subscribers("book:BTC") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("unsubscribe not applied")
		}
		time.Sleep(5 * time.Millisecond)
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

func TestWebsocketAccountTopicNeedsOwnersToken(t *testing.T) {
	owner := uuid.New()
	h := hub.New(func(string) (any, bool) { return nil, true })
	srv := httptest.NewServer(handleWS(h, func(_ context.Context, token string) (uuid.UUID, error) {
		if token == "owner-secret" {
			return owner, nil
		}
		return uuid.Nil, errors.New("unauthorized")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	topic := "account:" + owner.String()
	for _, token := range []string{"", "someone-else"} {
		buf, _ := json.Marshal(clientRequest{Op: "subscribe", Topic: topic, Token: token})
		conn.Write(ctx, websocket.MessageText, buf)
		_, reply, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(reply), "unauthorized") {
			t.Errorf("token %q: reply = %s, want unauthorized", token, reply)
		}
	}
	if h.Subscribers(topic) != 0 {
		t.Fatal("subscribed without the owner's token")
	}

	buf, _ := json.Marshal(clientRequest{Op: "subscribe", Topic: topic, Token: "owner-secret"})
	conn.Write(ctx, websocket.MessageText, buf)
	deadline := time.Now().Add(2 * time.Second)
	for h.Subscribers(topic) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("owner could not subscribe")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
