package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/juancruzfl/paperperps/server/internal/hub"
)

func TestWebsocketSubscribeFlow(t *testing.T) {
	h := hub.New(func(topic string) (any, bool) {
		if topic == "book:BTC" {
			return map[string]string{"coin": "BTC"}, true
		}
		return nil, false
	})
	srv := httptest.NewServer(handleWS(h))
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
