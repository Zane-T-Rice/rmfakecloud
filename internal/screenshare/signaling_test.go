package screenshare

import (
	"encoding/json"
	"testing"
	"time"
)

func signaling(kind string) json.RawMessage {
	return json.RawMessage(`{"type":"webtrc","payload":{"type":"` + kind + `"}}`)
}

func TestSignalingType(t *testing.T) {
	if got := SignalingType(Message{Payload: signaling("offer")}); got != "offer" {
		t.Fatalf("got %q", got)
	}
	if got := SignalingType(Message{Payload: json.RawMessage(`{"type":"candidate"}`)}); got != "candidate" {
		t.Fatalf("got %q", got)
	}
	if got := SignalingType(Message{Payload: json.RawMessage(`nope`)}); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestInner(t *testing.T) {
	m := Message{Payload: json.RawMessage(`{"type":"webtrc","payload":{"type":"candidate","candidate":"x","mid":"0"}}`)}
	in := Inner(m)
	if in["type"] != "candidate" || in["candidate"] != "x" {
		t.Fatalf("got %v", in)
	}
}

// the browser must get the candidates of the tablet with the offer, even when
// they come a little after it
func TestWaitForCandidatesHoldsTheOfferBack(t *testing.T) {
	rm := NewRoomManager()
	room := rm.CreateRoom("user", "tablet")

	// the request for the offer is the first message
	rm.AddBroadcast(room.RoomID, "browser", json.RawMessage(`{"type":"request-offer"}`))
	rm.AddDirect(room.RoomID, "tablet", "browser", signaling("offer"))
	go func() {
		time.Sleep(400 * time.Millisecond)
		rm.AddDirect(room.RoomID, "tablet", "browser", signaling("candidate"))
		rm.AddDirect(room.RoomID, "tablet", "browser", signaling("candidate"))
	}()

	msgs := rm.WaitForCandidates(room.RoomID, rm.GetMessages(room.RoomID, 1))
	candidates := 0
	for _, m := range msgs {
		if SignalingType(m) == "candidate" {
			candidates++
		}
	}
	if candidates != 2 {
		t.Fatalf("got %d candidates, want 2 (%d messages)", candidates, len(msgs))
	}
}

func TestWaitForCandidatesIgnoresOtherMessages(t *testing.T) {
	rm := NewRoomManager()
	room := rm.CreateRoom("user", "tablet")
	rm.AddBroadcast(room.RoomID, "browser", json.RawMessage(`{"type":"request-offer"}`))

	start := time.Now()
	msgs := rm.WaitForCandidates(room.RoomID, nil)
	if msgs != nil || time.Since(start) > time.Second {
		t.Fatalf("waited for candidates without an offer")
	}
}
