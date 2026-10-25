package screenshare

import (
	"reflect"
	"testing"
	"time"
)

func TestAllowOnlyForARoomAndNotTheOwner(t *testing.T) {
	rm := NewRoomManager()
	room := rm.CreateRoom("zane", "tablet")

	if rm.Allow("no-such-room", "anna") {
		t.Fatalf("allowed someone on a room that does not exist")
	}
	if rm.Allow(room.RoomID, "zane") {
		t.Fatalf("the owner was put on their own list")
	}
	if !rm.Allow(room.RoomID, "bob") || !rm.Allow(room.RoomID, "anna") {
		t.Fatalf("could not allow users")
	}
	if got := rm.AllowedUsers(room.RoomID); !reflect.DeepEqual(got, []string{"anna", "bob"}) {
		t.Fatalf("allowed = %v", got)
	}
	rm.Disallow(room.RoomID, "bob")
	if got := rm.AllowedUsers(room.RoomID); !reflect.DeepEqual(got, []string{"anna"}) {
		t.Fatalf("allowed after removing bob = %v", got)
	}
}

func TestCanWatch(t *testing.T) {
	rm := NewRoomManager()
	room := rm.CreateRoom("zane", "tablet")

	if !rm.CanWatch("zane", "zane") {
		t.Fatalf("the owner can't watch their own share")
	}
	if rm.CanWatch("zane", "anna") {
		t.Fatalf("anna may watch without being allowed")
	}
	rm.Allow(room.RoomID, "anna")
	if !rm.CanWatch("zane", "anna") {
		t.Fatalf("anna may not watch though allowed")
	}
	if rm.CanWatch("zane", "bob") {
		t.Fatalf("bob may watch though only anna is allowed")
	}
	if rm.CanWatch("nobody", "anna") {
		t.Fatalf("anna may watch a user who is not sharing")
	}
	rm.Disallow(room.RoomID, "anna")
	if rm.CanWatch("zane", "anna") {
		t.Fatalf("anna may still watch after being removed")
	}
}

// a new share starts with nobody allowed, what was allowed on the old one is over
func TestANewShareStartsWithNobodyAllowed(t *testing.T) {
	rm := NewRoomManager()
	old := rm.CreateRoom("zane", "tablet")
	rm.Allow(old.RoomID, "anna")

	time.Sleep(5 * time.Millisecond) // the newer room has to be newer
	rm.CreateRoom("zane", "tablet")

	if rm.CanWatch("zane", "anna") {
		t.Fatalf("the permission of the old share carried over")
	}
	if shared := rm.SharedWith("anna"); len(shared) != 0 {
		t.Fatalf("anna is told of a share: %v", shared)
	}
}

func TestSharedWith(t *testing.T) {
	rm := NewRoomManager()
	zane := rm.CreateRoom("zane", "tablet")
	other := rm.CreateRoom("carl", "tablet")
	rm.Allow(zane.RoomID, "anna")

	shared := rm.SharedWith("anna")
	if len(shared) != 1 || shared[0].OwnerID != "zane" || shared[0].RoomID != zane.RoomID {
		t.Fatalf("anna sees %v", shared)
	}
	if got := rm.SharedWith("bob"); len(got) != 0 {
		t.Fatalf("bob sees %v", got)
	}

	rm.Allow(other.RoomID, "anna")
	if got := rm.SharedWith("anna"); len(got) != 2 || got[0].OwnerID != "carl" || got[1].OwnerID != "zane" {
		t.Fatalf("anna sees %v, want carl and zane", got)
	}
}

func TestDropViewerLeavesTheOthers(t *testing.T) {
	s := newTestSession()
	s.handle(handshake(8, 6))
	owner, _ := s.addSubscriber("zane")
	anna, _ := s.addSubscriber("anna")
	anna2, _ := s.addSubscriber("anna")
	for _, sub := range []*Subscriber{owner, anna, anna2} {
		for len(sub.Messages) > 0 {
			<-sub.Messages
		}
	}

	s.relay.DropViewer("user", "anna")

	for _, sub := range []*Subscriber{anna, anna2} {
		select {
		case _, ok := <-sub.Messages:
			if ok {
				t.Fatalf("anna got a message after being dropped")
			}
		default:
			t.Fatalf("anna's channel is still open")
		}
	}
	s.handle([]byte{msgCursor, 0, 1, 0, 2})
	select {
	case m, ok := <-owner.Messages:
		if !ok || m[0] != msgCursor {
			t.Fatalf("the owner lost their stream")
		}
	default:
		t.Fatalf("the owner got nothing")
	}
}

// the tablet does not say when it stops, its keepalives stopping is the sign: a
// share it has stopped renewing is not offered to anyone, though the room is still there
func TestAShareThatIsNotRenewedIsOver(t *testing.T) {
	rm := NewRoomManager()
	room := rm.CreateRoom("zane", "tablet")
	rm.Allow(room.RoomID, "anna")

	if rm.FindActiveRoom("zane") != room.RoomID || !rm.CanWatch("zane", "anna") || len(rm.SharedWith("anna")) != 1 {
		t.Fatalf("a new share is not offered")
	}

	// no keepalive for longer than the window
	rm.mu.Lock()
	room.lastActivity = time.Now().UTC().Add(-liveWindow - time.Second)
	rm.mu.Unlock()

	if got := rm.FindActiveRoom("zane"); got != "" {
		t.Fatalf("the owner is offered a share that stopped: %s", got)
	}
	if rm.CanWatch("zane", "anna") {
		t.Fatalf("anna may watch a share that stopped")
	}
	if got := rm.SharedWith("anna"); len(got) != 0 {
		t.Fatalf("anna is told of a share that stopped: %v", got)
	}

	// a keepalive brings it back, it never was over
	rm.Keepalive(room.RoomID)
	if rm.FindActiveRoom("zane") != room.RoomID || !rm.CanWatch("zane", "anna") || len(rm.SharedWith("anna")) != 1 {
		t.Fatalf("a renewed share is not offered")
	}
}

func TestDeletedRoomIsNotOffered(t *testing.T) {
	rm := NewRoomManager()
	room := rm.CreateRoom("zane", "tablet")
	rm.Allow(room.RoomID, "anna")

	rm.DeleteRoom(room.RoomID)
	if rm.FindActiveRoom("zane") != "" || rm.CanWatch("zane", "anna") || len(rm.SharedWith("anna")) != 0 {
		t.Fatalf("a room that ended is offered")
	}
}
