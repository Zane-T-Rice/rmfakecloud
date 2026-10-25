package screenshare

import (
	"sort"
	"time"
)

// Allow lets the user watch the screen share of the room until it ends. It
// reports false when there is no such room.
func (rm *RoomManager) Allow(roomID, userID string) bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	room, exists := rm.rooms[roomID]
	if !exists || userID == room.ownerUserID {
		return false
	}
	room.allowed[userID] = struct{}{}
	return true
}

// Disallow takes the permission back
func (rm *RoomManager) Disallow(roomID, userID string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if room, exists := rm.rooms[roomID]; exists {
		delete(room.allowed, userID)
	}
}

// AllowedUsers are the users who may watch the room, besides its owner
func (rm *RoomManager) AllowedUsers(roomID string) []string {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	room, exists := rm.rooms[roomID]
	if !exists {
		return nil
	}
	users := make([]string, 0, len(room.allowed))
	for u := range room.allowed {
		users = append(users, u)
	}
	sort.Strings(users)
	return users
}

// SharedRoom is a screen share someone has allowed the user to watch
type SharedRoom struct {
	RoomID    string    `json:"roomId"`
	OwnerID   string    `json:"ownerId"`
	CreatedAt time.Time `json:"createdAt"`
}

// SharedWith lists the current screen shares the user has been allowed to watch,
// the newest one of each owner
func (rm *RoomManager) SharedWith(userID string) []SharedRoom {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	newest := make(map[string]*Room)
	now := time.Now().UTC()
	for _, room := range rm.rooms {
		if !room.live(now) {
			continue
		}
		if cur := newest[room.ownerUserID]; cur == nil || room.CreatedAt.After(cur.CreatedAt) {
			newest[room.ownerUserID] = room
		}
	}
	shared := []SharedRoom{}
	for owner, room := range newest {
		if _, ok := room.allowed[userID]; ok {
			shared = append(shared, SharedRoom{RoomID: room.RoomID, OwnerID: owner, CreatedAt: room.CreatedAt})
		}
	}
	sort.Slice(shared, func(i, j int) bool { return shared[i].OwnerID < shared[j].OwnerID })
	return shared
}

// CanWatch tells if the viewer may watch the current screen share of the owner:
// the owner always may, others only when allowed on that share
func (rm *RoomManager) CanWatch(ownerID, viewerID string) bool {
	if ownerID == viewerID {
		return true
	}
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	var newest *Room
	now := time.Now().UTC()
	for _, room := range rm.rooms {
		if room.ownerUserID == ownerID && room.live(now) && (newest == nil || room.CreatedAt.After(newest.CreatedAt)) {
			newest = room
		}
	}
	if newest == nil {
		return false
	}
	_, ok := newest.allowed[viewerID]
	return ok
}
