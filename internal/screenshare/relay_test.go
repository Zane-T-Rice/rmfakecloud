package screenshare

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"testing"
)

// rect is a piece of the screen in a frame, 16 bits per pixel
type rect struct {
	x, y, w, h int
	px         []byte
}

func solid(w, h int, v byte) []byte {
	return bytes.Repeat([]byte{v}, w*h*bytesPerPixel)
}

// frame builds a framebuffer message as the tablet sends it
func frame(rects ...rect) []byte {
	var raw []byte
	for _, r := range rects {
		hdr := make([]byte, rectHeaderSize)
		binary.BigEndian.PutUint16(hdr[0:], uint16(r.x))
		binary.BigEndian.PutUint16(hdr[2:], uint16(r.y))
		binary.BigEndian.PutUint16(hdr[4:], uint16(r.w))
		binary.BigEndian.PutUint16(hdr[6:], uint16(r.h))
		binary.BigEndian.PutUint32(hdr[8:], uint32(len(r.px)))
		raw = append(append(raw, hdr...), r.px...)
	}
	var packed bytes.Buffer
	zw := zlib.NewWriter(&packed)
	zw.Write(raw)
	zw.Close()

	msg := make([]byte, headerSize)
	msg[0] = msgFramebuffer
	binary.BigEndian.PutUint16(msg[1:], uint16(len(rects)))
	binary.BigEndian.PutUint32(msg[3:], uint32(packed.Len()))
	return append(msg, packed.Bytes()...)
}

func handshake(w, h int) []byte {
	msg := make([]byte, headerSize)
	msg[0] = msgHandshake
	binary.BigEndian.PutUint16(msg[3:], uint16(w))
	binary.BigEndian.PutUint16(msg[5:], uint16(h))
	return msg
}

func TestAssemblerPutsAFrameBackTogether(t *testing.T) {
	var a assembler
	msg := frame(rect{0, 0, 16, 16, solid(16, 16, 7)})
	if len(msg) < 30 {
		t.Fatalf("frame too small to split: %d", len(msg))
	}

	// in three pieces
	if got := a.process(msg[:20]); got != nil {
		t.Fatalf("incomplete frame came back")
	}
	if got := a.process(msg[20:30]); got != nil {
		t.Fatalf("incomplete frame came back")
	}
	got := a.process(msg[30:])
	if !bytes.Equal(got, msg) {
		t.Fatalf("frame not put back together: %d bytes, want %d", len(got), len(msg))
	}

	// and it takes the next one, whole
	other := handshake(4, 4)
	if got := a.process(other); !bytes.Equal(got, other) {
		t.Fatalf("other message changed")
	}
}

func TestScreenKeepsThePicture(t *testing.T) {
	var sc screen
	sc.apply(handshake(8, 6))
	if sc.snapshot() == nil || len(sc.pixels) != 8*6*bytesPerPixel {
		t.Fatalf("screen not set up: %d pixels bytes", len(sc.pixels))
	}

	// the whole screen, then a piece of it
	sc.apply(frame(rect{0, 0, 8, 6, solid(8, 6, 1)}))
	sc.apply(frame(rect{2, 1, 3, 2, solid(3, 2, 9)}))

	at := func(x, y int) byte { return sc.pixels[(y*8+x)*bytesPerPixel] }
	if at(0, 0) != 1 || at(7, 5) != 1 {
		t.Fatalf("whole frame not kept")
	}
	if at(2, 1) != 9 || at(4, 2) != 9 || at(5, 1) != 1 || at(2, 3) != 1 {
		t.Fatalf("partial frame in the wrong place")
	}
}

// a viewer that joins later gets what the screen shows, in the messages a page
// understands
func TestSnapshotRebuildsTheScreen(t *testing.T) {
	var sc screen
	sc.apply(handshake(8, 6))
	sc.apply(frame(rect{0, 0, 8, 6, solid(8, 6, 1)}))
	sc.apply(frame(rect{2, 1, 3, 2, solid(3, 2, 9)}))
	sc.apply([]byte{msgRotation, 0, 0, 0, 90})
	sc.apply([]byte{msgCursor, 0, 5, 0, 6})

	msgs := sc.snapshot()
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4 (handshake, frame, rotation, cursor)", len(msgs))
	}
	if msgs[0][0] != msgHandshake || msgs[1][0] != msgFramebuffer || msgs[2][0] != msgRotation || msgs[3][0] != msgCursor {
		t.Fatalf("wrong order of messages")
	}

	// playing the snapshot into a fresh screen gives the same picture
	var again screen
	for _, m := range msgs {
		again.apply(m)
	}
	if !bytes.Equal(again.pixels, sc.pixels) || again.width != 8 || again.height != 6 {
		t.Fatalf("snapshot does not give the same screen")
	}
}

func TestScreenFollowsAChangeOfSize(t *testing.T) {
	var sc screen
	sc.apply(handshake(8, 6))
	// a whole screen of another size, the page takes a frame of at least 90% of
	// the screen at the origin for that
	sc.apply(frame(rect{0, 0, 10, 7, solid(10, 7, 5)}))
	if sc.width != 10 || sc.height != 7 || len(sc.pixels) != 10*7*bytesPerPixel {
		t.Fatalf("size %dx%d with %d bytes", sc.width, sc.height, len(sc.pixels))
	}
	hs := sc.snapshot()[0]
	if binary.BigEndian.Uint16(hs[3:]) != 10 || binary.BigEndian.Uint16(hs[5:]) != 7 {
		t.Fatalf("handshake of a new viewer has the old size")
	}

	// a frame of the other shape is not a new size, the pixels of the screen stay
	before := append([]byte(nil), sc.pixels...)
	sc.apply(frame(rect{0, 0, 3, 9, solid(3, 9, 8)}))
	if sc.width != 10 || sc.height != 7 || len(sc.pixels) != len(before) {
		t.Fatalf("a small frame changed the size to %dx%d", sc.width, sc.height)
	}
}

func TestBrokenMessagesAreIgnored(t *testing.T) {
	var sc screen
	sc.apply(nil)
	sc.apply([]byte{msgFramebuffer})  // no screen yet
	sc.apply([]byte{msgHandshake, 0}) // short
	sc.apply(handshake(8, 6))
	sc.apply([]byte{msgFramebuffer, 0, 1, 0, 0, 0, 200})                // more than there is
	sc.apply(append([]byte{msgFramebuffer, 0, 1, 0, 0, 0, 3}, 1, 2, 3)) // not zlib
	if len(sc.snapshot()) != 2 {
		t.Fatalf("a broken message changed the screen")
	}
}

func newTestSession() *session {
	r := &Relay{sessions: map[string]*session{}}
	s := newSession(r, "user")
	r.sessions["user"] = s
	return s
}

func TestSubscribersGetTheStateAndWhatFollows(t *testing.T) {
	s := newTestSession()
	s.handle(handshake(8, 6))
	s.handle(frame(rect{0, 0, 8, 6, solid(8, 6, 1)}))

	// the first viewer
	a, err := s.addSubscriber("user")
	if err != nil {
		t.Fatal(err)
	}
	update := frame(rect{1, 1, 2, 2, solid(2, 2, 4)})
	s.handle(update)

	// one that comes later still sees the update
	b, err := s.addSubscriber("user")
	if err != nil {
		t.Fatal(err)
	}

	var got []([]byte)
	for len(a.Messages) > 0 {
		got = append(got, <-a.Messages)
	}
	if len(got) != 3 || got[0][0] != msgHandshake || !bytes.Equal(got[2], update) {
		t.Fatalf("first viewer got %d messages", len(got))
	}

	var late screen
	for len(b.Messages) > 0 {
		late.apply(<-b.Messages)
	}
	if !bytes.Equal(late.pixels, s.screen.pixels) {
		t.Fatalf("late viewer has another picture")
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	s := newTestSession()
	s.handle(handshake(8, 6))
	slow, _ := s.addSubscriber("user")
	fast, _ := s.addSubscriber("user")

	for i := 0; i < subscriberBuffer+10; i++ {
		s.handle([]byte{msgCursor, 0, 1, 0, 2})
		for len(fast.Messages) > 0 {
			<-fast.Messages
		}
	}

	// the channel of the slow one is closed, and drained it ends
	n := 0
	for range slow.Messages {
		n++
	}
	if n == 0 || n > subscriberBuffer {
		t.Fatalf("slow viewer got %d messages", n)
	}
	select {
	case _, ok := <-fast.Messages:
		if !ok {
			t.Fatalf("the fast viewer was dropped")
		}
	default:
	}
	fast.Close()
	slow.Close() // already gone, no panic
}

func TestFinishClosesTheViewers(t *testing.T) {
	s := newTestSession()
	s.handle(handshake(8, 6))
	sub, _ := s.addSubscriber("user")
	for len(sub.Messages) > 0 {
		<-sub.Messages
	}

	s.fail(nil)
	if _, ok := <-sub.Messages; ok {
		t.Fatalf("viewer not closed")
	}
	if _, err := s.addSubscriber("user"); err == nil {
		t.Fatalf("joined a session that is over")
	}
	if !s.isDone() {
		t.Fatalf("not done")
	}
	if s.relay.sessions["user"] != nil {
		t.Fatalf("the session is still listed")
	}
}

// the tablet's streams may end without the closing block, the page takes those
func frameUnclosed(rects ...rect) []byte {
	var raw []byte
	for _, r := range rects {
		hdr := make([]byte, rectHeaderSize)
		binary.BigEndian.PutUint16(hdr[0:], uint16(r.x))
		binary.BigEndian.PutUint16(hdr[2:], uint16(r.y))
		binary.BigEndian.PutUint16(hdr[4:], uint16(r.w))
		binary.BigEndian.PutUint16(hdr[6:], uint16(r.h))
		binary.BigEndian.PutUint32(hdr[8:], uint32(len(r.px)))
		raw = append(append(raw, hdr...), r.px...)
	}
	var packed bytes.Buffer
	zw := zlib.NewWriter(&packed)
	zw.Write(raw)
	zw.Flush() // flushed, never closed

	msg := make([]byte, headerSize)
	msg[0] = msgFramebuffer
	binary.BigEndian.PutUint16(msg[1:], uint16(len(rects)))
	binary.BigEndian.PutUint32(msg[3:], uint32(packed.Len()))
	return append(msg, packed.Bytes()...)
}

func TestScreenTakesFramesWithoutTheClosingBlock(t *testing.T) {
	var sc screen
	sc.apply(handshake(8, 6))
	if err := sc.apply(frameUnclosed(rect{0, 0, 8, 6, solid(8, 6, 3)})); err != nil {
		t.Fatalf("frame not taken: %v", err)
	}
	if sc.pixels[0] != 3 || sc.pixels[len(sc.pixels)-1] != 3 {
		t.Fatalf("the picture is missing")
	}
}

func TestUnusableFramesSayWhy(t *testing.T) {
	var sc screen
	if err := sc.apply(frame(rect{0, 0, 2, 2, solid(2, 2, 1)})); err == nil {
		t.Fatalf("a frame before the screen is known was taken")
	}
	sc.apply(handshake(8, 6))
	if err := sc.apply(append([]byte{msgFramebuffer, 0, 1, 0, 0, 0, 3}, 1, 2, 3)); err == nil {
		t.Fatalf("a frame that is not zlib was taken")
	}
}
