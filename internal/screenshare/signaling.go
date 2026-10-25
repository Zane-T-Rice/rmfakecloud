package screenshare

import (
	"encoding/json"
	"time"

	log "github.com/sirupsen/logrus"
)

// SignalingType is the type of a webrtc signaling message of the tablet: offer,
// candidate...
func SignalingType(m Message) string {
	var p struct {
		Type    string `json:"type"`
		Payload struct {
			Type string `json:"type"`
		} `json:"payload"`
	}
	if json.Unmarshal(m.Payload, &p) != nil {
		return ""
	}
	if p.Type == "webtrc" && p.Payload.Type != "" {
		return p.Payload.Type
	}
	return p.Type
}

// Inner is the webrtc message inside a signaling message
func Inner(m Message) map[string]interface{} {
	var p map[string]interface{}
	if json.Unmarshal(m.Payload, &p) != nil {
		return nil
	}
	if p["type"] == "webtrc" {
		if in, ok := p["payload"].(map[string]interface{}); ok {
			return in
		}
	}
	return p
}

// candidateWait is how long to wait for the tablet's candidates after its offer
const candidateWait = 4 * time.Second

// WaitForCandidates holds the offer back until the tablet's ice candidates have
// arrived with it. The offer has none in it, they follow as messages, and a
// browser given the offer alone has no address to connect to.
func (rm *RoomManager) WaitForCandidates(roomID string, msgs []Message) []Message {
	has := func(msgs []Message, t string) bool {
		for _, m := range msgs {
			if SignalingType(m) == t {
				return true
			}
		}
		return false
	}
	if !has(msgs, "offer") {
		return msgs
	}

	deadline := time.Now().Add(candidateWait)
	for !has(msgs, "candidate") && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		msgs = rm.GetMessages(roomID, 1)
	}
	if has(msgs, "candidate") {
		// the others follow close behind the first
		time.Sleep(300 * time.Millisecond)
		msgs = rm.GetMessages(roomID, 1)
	} else {
		log.Warn("Screenshare: no candidates from the tablet with its offer in room ", roomID)
	}
	return msgs
}
