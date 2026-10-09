package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/juancruzfl/paperperps/server/internal/hub"
)

const (
	wsSendBuffer   = 256
	wsWriteTimeout = 10 * time.Second
	wsPingInterval = 30 * time.Second
)

// clientRequest is what browsers send: {"op":"subscribe","topic":"book:BTC"}. Private
// account topics ("account:<id>") also carry the account's secret as token.
type clientRequest struct {
	Op    string `json:"op"`
	Topic string `json:"topic"`
	Token string `json:"token,omitempty"`
}

// Authenticator resolves an account secret to its account ID.
type Authenticator func(ctx context.Context, secret string) (uuid.UUID, error)

const accountTopicPrefix = "account:"

type wsError struct {
	Op    string `json:"op"`
	Topic string `json:"topic"`
	Error string `json:"error"`
}

// handleWS serves the client websocket. One goroutine reads subscribe and unsubscribe
// requests; this one writes hub messages and keepalive pings.
func handleWS(h *hub.Hub, auth Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Accept rejects cross-origin requests by default; the app is same-origin.
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(4 << 10)

		client := h.NewClient(wsSendBuffer)
		defer h.Remove(client)

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		go func() {
			defer cancel()
			readRequests(ctx, conn, h, client, auth)
		}()

		ping := time.NewTicker(wsPingInterval)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-client.Done:
				conn.Close(websocket.StatusPolicyViolation, "client too slow")
				return
			case msg := <-client.Send:
				if err := write(ctx, conn, msg); err != nil {
					return
				}
			case <-ping.C:
				pingCtx, cancelPing := context.WithTimeout(ctx, wsWriteTimeout)
				err := conn.Ping(pingCtx)
				cancelPing()
				if err != nil {
					return
				}
			}
		}
	}
}

func readRequests(ctx context.Context, conn *websocket.Conn, h *hub.Hub, client *hub.Client, auth Authenticator) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var req clientRequest
		if err := json.Unmarshal(data, &req); err != nil {
			replyError(ctx, conn, req, "invalid json")
			continue
		}
		switch req.Op {
		case "subscribe":
			if strings.HasPrefix(req.Topic, accountTopicPrefix) && !ownsAccountTopic(ctx, auth, req) {
				replyError(ctx, conn, req, "unauthorized")
				continue
			}
			if err := h.Subscribe(client, req.Topic); err != nil {
				if errors.Is(err, hub.ErrClientIsClosed) {
					return
				}
				replyError(ctx, conn, req, err.Error())
			}
		case "unsubscribe":
			h.Unsubscribe(client, req.Topic)
		default:
			replyError(ctx, conn, req, "unknown op")
		}
	}
}

// ownsAccountTopic checks that the request's token belongs to the account in its topic.
func ownsAccountTopic(ctx context.Context, auth Authenticator, req clientRequest) bool {
	want, err := uuid.Parse(strings.TrimPrefix(req.Topic, accountTopicPrefix))
	if err != nil {
		return false
	}
	id, err := auth(ctx, req.Token)
	return err == nil && id == want
}

func replyError(ctx context.Context, conn *websocket.Conn, req clientRequest, msg string) {
	buf, _ := json.Marshal(hub.Message{Topic: "error", Data: wsError{Op: req.Op, Topic: req.Topic, Error: msg}})
	if err := write(ctx, conn, buf); err != nil {
		slog.Debug("ws: write error reply", "err", err)
	}
}

func write(ctx context.Context, conn *websocket.Conn, msg []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, msg)
}
