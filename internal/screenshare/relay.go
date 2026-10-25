package screenshare

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
	log "github.com/sirupsen/logrus"
)

// The relay is the one viewer of the tablet's screen share, the browsers get the
// picture from it over a websocket. A browser connecting to the tablet by itself
// only works when it can reach the tablet with UDP (not from a phone on Wi-Fi
// here), and the tablet serves a single viewer.

// the messages of the tablet (the first byte)
const (
	msgFramebuffer = 0
	msgCursor      = 100
	msgRotation    = 102
	msgHandshake   = 104

	// [type(1), rectCount(2 BE), deflatedSize(4 BE), zlib...] and for a handshake
	// [type(1), ?(2), width(2 BE), height(2 BE)]
	headerSize = 7
	// [x(2), y(2), w(2), h(2), pixelDataLen(4), pixels...] in a frame, inflated
	rectHeaderSize = 12
	// 16 bits per pixel
	bytesPerPixel = 2

	maxPixels     = 16 << 20
	maxFrameBytes = 64 << 20

	subscriberBuffer = 512
	// how long the tablet is kept connected without a browser
	idleTimeout = 30 * time.Second
	// from the request of the offer to the first data from the tablet
	connectTimeout = 60 * time.Second
	dataTimeout    = 25 * time.Second
	gatherTimeout  = 5 * time.Second
)

// ErrNoRoom there is no screen share to join
var ErrNoRoom = errors.New("no active room")

// Signaler is how the relay talks to the tablet, the same way a browser does
type Signaler interface {
	// RequestOffer asks the tablet for an offer for the client
	RequestOffer(uid, roomID, clientID string)
	// SendDirect sends a message (the answer) to the tablet
	SendDirect(uid, roomID, clientID, targetClientID string, payload json.RawMessage)
}

// Relay keeps one connection to the tablet per user
type Relay struct {
	rooms *RoomManager
	sig   Signaler
	ice   []webrtc.ICEServer

	mu       sync.Mutex
	sessions map[string]*session
}

// NewRelay makes a relay, iceServers are those of the config (maps with url,
// username and credential)
func NewRelay(rooms *RoomManager, sig Signaler, iceServers []interface{}) *Relay {
	return &Relay{
		rooms:    rooms,
		sig:      sig,
		ice:      toICEServers(iceServers),
		sessions: make(map[string]*session),
	}
}

func toICEServers(servers []interface{}) []webrtc.ICEServer {
	var out []webrtc.ICEServer
	str := func(m map[string]interface{}, k string) string {
		s, _ := m[k].(string)
		return s
	}
	for _, s := range servers {
		var m map[string]interface{}
		switch t := s.(type) {
		case map[string]interface{}:
			m = t
		case map[string]string:
			m = make(map[string]interface{}, len(t))
			for k, v := range t {
				m[k] = v
			}
		default:
			continue
		}
		url := str(m, "url")
		if url == "" {
			url = str(m, "urls")
		}
		if url == "" {
			continue
		}
		out = append(out, webrtc.ICEServer{
			URLs:       []string{url},
			Username:   str(m, "username"),
			Credential: str(m, "credential"),
		})
	}
	return out
}

// Subscribe joins the screen share of the user, connecting to the tablet first if
// needed. Wait on Messages: the first ones are the current state of the screen.
func (r *Relay) Subscribe(uid string) (*Subscriber, error) {
	r.mu.Lock()
	s := r.sessions[uid]
	if s != nil && s.isDone() {
		s = nil
	}
	if s == nil {
		s = newSession(r, uid)
		r.sessions[uid] = s
		go s.run()
	}
	r.mu.Unlock()

	select {
	case <-s.ready:
	case <-time.After(connectTimeout):
		return nil, errors.New("timeout connecting to the tablet")
	}
	if err := s.failure(); err != nil {
		return nil, err
	}
	return s.addSubscriber()
}

func (r *Relay) forget(s *session) {
	r.mu.Lock()
	if r.sessions[s.uid] == s {
		delete(r.sessions, s.uid)
	}
	r.mu.Unlock()
}

// Subscriber gets the messages of the tablet
type Subscriber struct {
	// Messages is closed when the screen share has ended
	Messages chan []byte
	s        *session
}

// Close leaves the screen share
func (sub *Subscriber) Close() {
	sub.s.removeSubscriber(sub)
}

type session struct {
	relay    *Relay
	uid      string
	clientID string

	ready     chan struct{}
	done      chan struct{}
	readyOnce sync.Once
	doneOnce  sync.Once

	pc *webrtc.PeerConnection
	dc *webrtc.DataChannel
	// only the data channel goroutine
	asm assembler

	mu     sync.Mutex
	err    error
	screen screen
	// frames that could not be used, only the first are logged
	unusable int
	subs     map[*Subscriber]struct{}
	idle     *time.Timer
}

func newSession(r *Relay, uid string) *session {
	return &session{
		relay:    r,
		uid:      uid,
		clientID: uuid.NewString(),
		ready:    make(chan struct{}),
		done:     make(chan struct{}),
		subs:     make(map[*Subscriber]struct{}),
	}
}

func (s *session) isDone() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *session) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *session) markReady() {
	s.readyOnce.Do(func() { close(s.ready) })
}

// fail ends the session, with the reason when it is the first
func (s *session) fail(err error) {
	s.mu.Lock()
	if s.err == nil && err != nil {
		s.err = err
		log.Warnf("Screenshare relay of %s: %v", s.uid, err)
	}
	s.mu.Unlock()
	s.markReady()
	s.finish()
}

func (s *session) finish() {
	s.doneOnce.Do(func() {
		close(s.done)
		s.relay.forget(s)

		s.mu.Lock()
		if s.idle != nil {
			s.idle.Stop()
		}
		for sub := range s.subs {
			close(sub.Messages)
			delete(s.subs, sub)
		}
		s.mu.Unlock()

		if s.pc != nil {
			s.pc.Close()
		}
	})
}

// stop tells the tablet it has no viewer anymore (what the browser does when it
// disconnects) and closes
func (s *session) stop() {
	if s.dc != nil && s.dc.ReadyState() == webrtc.DataChannelStateOpen {
		s.dc.Send([]byte{0, 0, 0, 0x65})
	}
	s.finish()
}

func (s *session) run() {
	if err := s.connect(); err != nil {
		s.fail(err)
	}
}

func (s *session) connect() error {
	rooms := s.relay.rooms
	roomID := rooms.FindActiveRoom(s.uid)
	if roomID == "" {
		return ErrNoRoom
	}

	s.relay.sig.RequestOffer(s.uid, roomID, s.clientID)
	msgs := rooms.WaitForMessages(roomID, 1, 30*time.Second)
	if msgs == nil {
		return errors.New("the tablet did not send an offer")
	}
	msgs = rooms.WaitForCandidates(roomID, msgs)

	var offer, tabletID string
	var candidates []map[string]interface{}
	for _, m := range msgs {
		if m.TargetClientID != "" && m.TargetClientID != s.clientID {
			continue
		}
		in := Inner(m)
		switch in["type"] {
		case "offer":
			offer, _ = in["description"].(string)
			if offer == "" {
				offer, _ = in["sdp"].(string)
			}
			tabletID = m.SenderClientID
		case "candidate":
			candidates = append(candidates, in)
		}
	}
	if offer == "" {
		return errors.New("no offer from the tablet")
	}
	log.Infof("Screenshare relay of %s: offer with %d candidates", s.uid, len(candidates))

	// our own address is the server's, not a name no one can look up
	se := webrtc.SettingEngine{}
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: s.relay.ice})
	if err != nil {
		return fmt.Errorf("peer connection: %w", err)
	}
	s.pc = pc

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Infof("Screenshare relay of %s: connection %s", s.uid, state)
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			s.fail(fmt.Errorf("connection to the tablet %s", state))
		}
	})
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Infof("Screenshare relay of %s: ice %s", s.uid, state)
	})
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		s.dc = dc
		dc.OnOpen(func() {
			// what the browser sends when the channel opens
			hello := append([]byte("reMarkable"), 0, 0)
			if err := dc.Send(hello); err != nil {
				s.fail(fmt.Errorf("hello: %w", err))
			}
		})
		dc.OnMessage(func(m webrtc.DataChannelMessage) {
			if m.IsString {
				return
			}
			s.handle(append([]byte(nil), m.Data...))
		})
		dc.OnClose(func() {
			s.fail(errors.New("the tablet closed the data channel"))
		})
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return fmt.Errorf("offer: %w", err)
	}
	for _, c := range candidates {
		cand, _ := c["candidate"].(string)
		cand = strings.TrimPrefix(cand, "a=")
		if cand == "" {
			continue
		}
		mid, _ := c["mid"].(string)
		if mid == "" {
			mid = "0"
		}
		index := uint16(0)
		if err := pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: cand, SDPMid: &mid, SDPMLineIndex: &index}); err != nil {
			log.Warnf("Screenshare relay of %s: candidate %q: %v", s.uid, cand, err)
		}
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("local description: %w", err)
	}
	select {
	case <-gathered:
	case <-time.After(gatherTimeout):
	}

	payload, err := json.Marshal(map[string]interface{}{
		"type": "webtrc",
		"payload": map[string]interface{}{
			"type":        "answer",
			"description": pc.LocalDescription().SDP,
		},
	})
	if err != nil {
		return err
	}
	s.relay.sig.SendDirect(s.uid, roomID, s.clientID, tabletID, payload)

	// the tablet must start sending, or the connection did not work
	time.AfterFunc(dataTimeout, func() {
		select {
		case <-s.ready:
		default:
			s.fail(errors.New("no data from the tablet (is it reachable from the server?)"))
		}
	})
	return nil
}

// handle gets a message of the data channel
func (s *session) handle(data []byte) {
	complete := s.asm.process(data)
	if complete == nil || len(complete) == 0 {
		return
	}

	s.mu.Lock()
	if complete[0] == msgHandshake {
		s.markReady()
	}
	if err := s.screen.apply(complete); err != nil && s.unusable < 5 {
		s.unusable++
		log.Warnf("Screenshare relay of %s: %v", s.uid, err)
	}
	for sub := range s.subs {
		select {
		case sub.Messages <- complete:
		default:
			// it can't keep up and would show a picture missing some updates, it
			// has to start again
			delete(s.subs, sub)
			close(sub.Messages)
		}
	}
	s.mu.Unlock()
}

func (s *session) addSubscriber() (*Subscriber, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isDone() {
		return nil, errors.New("the screen share has ended")
	}
	sub := &Subscriber{Messages: make(chan []byte, subscriberBuffer), s: s}
	for _, m := range s.screen.snapshot() {
		sub.Messages <- m
	}
	s.subs[sub] = struct{}{}
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
	return sub, nil
}

func (s *session) removeSubscriber(sub *Subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[sub]; !ok {
		return
	}
	delete(s.subs, sub)
	close(sub.Messages)
	if len(s.subs) == 0 && !s.isDone() {
		s.idle = time.AfterFunc(idleTimeout, func() {
			s.mu.Lock()
			unused := len(s.subs) == 0
			s.mu.Unlock()
			if unused {
				s.stop()
			}
		})
	}
}

// assembler puts the frames the tablet splits over several messages back together,
// the other messages pass untouched
type assembler struct {
	buf      []byte
	expected int
	got      int
}

// process gives the complete message, or nil while a frame is still arriving
func (a *assembler) process(data []byte) []byte {
	if a.buf != nil {
		return a.append(data)
	}
	if len(data) < headerSize || data[0] != msgFramebuffer {
		return data
	}
	total := headerSize + int(binary.BigEndian.Uint32(data[3:7]))
	if len(data) >= total {
		return data
	}
	if total > maxFrameBytes {
		return nil
	}
	a.expected = total
	a.buf = make([]byte, total)
	copy(a.buf, data)
	a.got = len(data)
	return nil
}

func (a *assembler) append(chunk []byte) []byte {
	take := a.expected - a.got
	if len(chunk) < take {
		take = len(chunk)
	}
	copy(a.buf[a.got:], chunk[:take])
	a.got += take
	if a.got < a.expected {
		return nil
	}
	done := a.buf
	*a = assembler{}
	return done
}

// screen is the picture on the tablet as far as the messages tell, so a viewer
// that joins later gets it at once
type screen struct {
	handshake []byte
	width     int
	height    int
	// 16 bits per pixel, as in the frames
	pixels   []byte
	rotation []byte
	cursor   []byte
}

func (sc *screen) resize(w, h int) {
	sc.width, sc.height = w, h
	if w <= 0 || h <= 0 || w*h > maxPixels {
		sc.pixels = nil
		return
	}
	sc.pixels = make([]byte, w*h*bytesPerPixel)
}

// apply takes a complete message of the tablet, the error says why a frame could
// not be used
func (sc *screen) apply(msg []byte) error {
	if len(msg) == 0 {
		return nil
	}
	switch msg[0] {
	case msgHandshake:
		if len(msg) < headerSize {
			return nil
		}
		sc.handshake = append([]byte(nil), msg...)
		sc.resize(int(binary.BigEndian.Uint16(msg[3:5])), int(binary.BigEndian.Uint16(msg[5:7])))
	case msgRotation:
		sc.rotation = append([]byte(nil), msg...)
	case msgCursor:
		sc.cursor = append([]byte(nil), msg...)
	case msgFramebuffer:
		return sc.applyFrame(msg)
	}
	return nil
}

// inflate unpacks a frame. The tablet's streams may end without the closing block
// (the page's inflate takes them), what was read before that is all there is.
func inflate(packed []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	raw, err := io.ReadAll(io.LimitReader(zr, maxFrameBytes))
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return raw, nil
}

func (sc *screen) applyFrame(msg []byte) error {
	if sc.pixels == nil || len(msg) < headerSize {
		return errors.New("a frame before the screen is known")
	}
	size := int(binary.BigEndian.Uint32(msg[3:7]))
	if headerSize+size > len(msg) {
		return fmt.Errorf("a frame of %d bytes in %d", size, len(msg)-headerSize)
	}
	raw, err := inflate(msg[headerSize : headerSize+size])
	if err != nil {
		return fmt.Errorf("a frame that does not inflate: %w", err)
	}

	for pos := 0; pos+rectHeaderSize <= len(raw); {
		x := int(binary.BigEndian.Uint16(raw[pos:]))
		y := int(binary.BigEndian.Uint16(raw[pos+2:]))
		w := int(binary.BigEndian.Uint16(raw[pos+4:]))
		h := int(binary.BigEndian.Uint16(raw[pos+6:]))
		n := int(binary.BigEndian.Uint32(raw[pos+8:]))
		pos += rectHeaderSize
		if pos+n > len(raw) {
			return errors.New("a rect that is cut short")
		}
		px := raw[pos : pos+n]
		pos += n
		if w <= 0 || h <= 0 || n < w*h*bytesPerPixel {
			return fmt.Errorf("a rect of %dx%d with %d bytes of pixels", w, h, n)
		}
		// a (nearly) whole screen at the origin is the new size of the screen,
		// that is how the page takes it too
		if x == 0 && y == 0 && float64(w) >= float64(sc.width)*0.9 && float64(h) >= float64(sc.height)*0.9 {
			if w != sc.width || h != sc.height {
				sc.resize(w, h)
				if sc.pixels == nil {
					return fmt.Errorf("a screen of %dx%d", w, h)
				}
			}
		}
		for row := 0; row < h; row++ {
			dy := y + row
			if dy >= sc.height || x >= sc.width {
				break
			}
			cols := w
			if x+cols > sc.width {
				cols = sc.width - x
			}
			copy(sc.pixels[(dy*sc.width+x)*bytesPerPixel:], px[row*w*bytesPerPixel:(row*w+cols)*bytesPerPixel])
		}
	}
	return nil
}

// snapshot is the messages that give a new viewer what the screen shows now
func (sc *screen) snapshot() [][]byte {
	if sc.handshake == nil {
		return nil
	}
	hs := append([]byte(nil), sc.handshake...)
	binary.BigEndian.PutUint16(hs[3:], uint16(sc.width))
	binary.BigEndian.PutUint16(hs[5:], uint16(sc.height))
	out := [][]byte{hs}

	if sc.pixels != nil {
		if frame := sc.wholeFrame(); frame != nil {
			out = append(out, frame)
		}
	}
	if sc.rotation != nil {
		out = append(out, append([]byte(nil), sc.rotation...))
	}
	if sc.cursor != nil {
		out = append(out, append([]byte(nil), sc.cursor...))
	}
	return out
}

// wholeFrame is a frame with the whole screen in one rect
func (sc *screen) wholeFrame() []byte {
	raw := make([]byte, rectHeaderSize, rectHeaderSize+len(sc.pixels))
	binary.BigEndian.PutUint16(raw[4:], uint16(sc.width))
	binary.BigEndian.PutUint16(raw[6:], uint16(sc.height))
	binary.BigEndian.PutUint32(raw[8:], uint32(len(sc.pixels)))
	raw = append(raw, sc.pixels...)

	var packed bytes.Buffer
	zw, err := zlib.NewWriterLevel(&packed, zlib.BestSpeed)
	if err != nil {
		return nil
	}
	if _, err := zw.Write(raw); err != nil {
		return nil
	}
	if err := zw.Close(); err != nil {
		return nil
	}

	msg := make([]byte, headerSize, headerSize+packed.Len())
	msg[0] = msgFramebuffer
	binary.BigEndian.PutUint16(msg[1:], 1)
	binary.BigEndian.PutUint32(msg[3:], uint32(packed.Len()))
	return append(msg, packed.Bytes()...)
}
