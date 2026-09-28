package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Q-xuan/GameLink/internal/config"
)

// CreateRoom calls POST /v1/rooms.
func CreateRoom(ctx context.Context, controlURL string) (Created, error) {
	base, err := config.HTTPBase(controlURL)
	if err != nil {
		return Created{}, err
	}
	var out Created
	if err := doJSON(ctx, http.MethodPost, base+"/v1/rooms", nil, &out, http.StatusCreated); err != nil {
		return Created{}, err
	}
	return out, nil
}

// JoinRoom calls POST /v1/rooms/{code}/join.
func JoinRoom(ctx context.Context, controlURL, code, token string) (Joined, error) {
	base, err := config.HTTPBase(controlURL)
	if err != nil {
		return Joined{}, err
	}
	var out Joined
	path := base + "/v1/rooms/" + url.PathEscape(code) + "/join"
	if err := doJSON(ctx, http.MethodPost, path, joinRequest{Token: token}, &out, http.StatusOK); err != nil {
		return Joined{}, err
	}
	return out, nil
}

// DialEvents opens the presence websocket for a joined peer.
func DialEvents(ctx context.Context, controlURL, code, token string, peerID uint32) (*websocket.Conn, error) {
	base, err := config.WSBase(controlURL)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("token", token)
	q.Set("peer_id", strconv.FormatUint(uint64(peerID), 10))
	u := base + "/v1/rooms/" + url.PathEscape(code) + "/ws?" + q.Encode()
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u, nil)
	return conn, err
}

func doJSON(ctx context.Context, method, rawURL string, in any, out any, want int) error {
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != want {
		return fmt.Errorf("http %d: %s", resp.StatusCode, bytes.TrimSpace(buf))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(buf, out)
}
