package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Q-xuan/GameLink/internal/peer"
	"github.com/Q-xuan/GameLink/internal/room"
)

func testServer(t *testing.T, logger *log.Logger) (*room.Store, *Hub, *httptest.Server) {
	t.Helper()
	rooms := room.NewStore()
	hub := NewHub(rooms)
	srv := New(Options{
		Rooms:            rooms,
		Hub:              hub,
		PublicRelay:      "127.0.0.1:41000",
		PublicControlURL: "http://127.0.0.1:41080",
		Logger:           logger,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return rooms, hub, ts
}

func TestHTTPTimeoutCoversProxy(t *testing.T) {
	if httpTimeout < 10*time.Second {
		t.Fatalf("http timeout %s is below 10s", httpTimeout)
	}
}

func TestHealthz(t *testing.T) {
	_, _, ts := testServer(t, nil)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != `{"status":"ok"}` {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
}

func TestCreateJoinFullAndToken(t *testing.T) {
	_, _, ts := testServer(t, nil)
	resp, err := http.Post(ts.URL+"/v1/rooms", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatal(resp.StatusCode)
	}
	var created Created
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.PeerID != 1 || created.VIP != "10.66.0.1" || created.Relay != "127.0.0.1:41000" {
		t.Fatalf("%+v", created)
	}
	if created.ControlURL != "http://127.0.0.1:41080" || len(created.Token) != 32 || len(created.Code) != 6 {
		t.Fatalf("%+v", created)
	}
	joined, err := JoinRoom(context.Background(), ts.URL, created.Code, created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if joined.PeerID != 2 || joined.VIP != "10.66.0.2" || joined.Relay != created.Relay || joined.RoomID != created.RoomID {
		t.Fatalf("%+v", joined)
	}
	if _, err := JoinRoom(context.Background(), ts.URL, created.Code, "00112233445566778899aabbccddeeff"); err == nil {
		t.Fatal("expected bad token")
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/rooms/NO-SUCH/join", strings.NewReader(`{"token":"`+created.Token+`"}`))
	req.Header.Set("Content-Type", "application/json")
	bad, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad code %d", bad.StatusCode)
	}
	missing, err := http.Post(ts.URL+"/v1/rooms/ABCDEF/join", "application/json", strings.NewReader(`{"token":"`+created.Token+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing %d", missing.StatusCode)
	}
	for i := 0; i < peer.MaxPeers-2; i++ {
		if _, err := JoinRoom(context.Background(), ts.URL, created.Code, created.Token); err != nil {
			t.Fatal(err)
		}
	}
	fullReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/rooms/"+created.Code+"/join", strings.NewReader(`{"token":"`+created.Token+`"}`))
	fullResp, err := http.DefaultClient.Do(fullReq)
	if err != nil {
		t.Fatal(err)
	}
	defer fullResp.Body.Close()
	if fullResp.StatusCode != http.StatusConflict {
		t.Fatalf("full %d", fullResp.StatusCode)
	}
}

func TestForwardedForIsLogOnly(t *testing.T) {
	var buf bytes.Buffer
	_, _, ts := testServer(t, log.New(&buf, "", 0))
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/rooms", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var created Created
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.VIP != "10.66.0.1" || created.PeerID != 1 {
		t.Fatalf("%+v", created)
	}
	if !strings.Contains(buf.String(), "203.0.113.5") {
		t.Fatalf("log %q", buf.String())
	}
	if strings.Contains(created.VIP, "203.0.113") {
		t.Fatal(created.VIP)
	}
}

func TestWebSocketPresence(t *testing.T) {
	rooms, hub, ts := testServer(t, nil)
	created, err := CreateRoom(context.Background(), ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := DialEvents(ctx, ts.URL, created.Code, created.Token, created.PeerID)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var snap Event
	if err := conn.ReadJSON(&snap); err != nil {
		t.Fatal(err)
	}
	if snap.Type != "snapshot" || len(snap.Peers) != 1 || snap.Peers[0].PeerID != 1 || snap.Peers[0].Online {
		t.Fatalf("%+v", snap)
	}
	p, ok := rooms.Get(parseID(t, created.RoomID), 1)
	if !ok {
		t.Fatal("missing peer")
	}
	hub.PeerUp(p)
	var up Event
	if err := conn.ReadJSON(&up); err != nil {
		t.Fatal(err)
	}
	if up.Type != "peer_online" || up.PeerID != 1 || up.Online == nil || !*up.Online {
		t.Fatalf("%+v", up)
	}
	hub.PeerDown(p)
	var down Event
	if err := conn.ReadJSON(&down); err != nil {
		t.Fatal(err)
	}
	if down.Type != "peer_offline" || down.PeerID != 1 || down.Online == nil || *down.Online {
		t.Fatalf("%+v", down)
	}
}

func parseID(t *testing.T, s string) uint64 {
	t.Helper()
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
