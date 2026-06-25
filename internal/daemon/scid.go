package daemon

import (
	"fmt"
	"sync"

	phonelog "github.com/gezihua123/phonefast/internal/log"
)

// scrcpy's video port is derived from scid via the same hash used in
// internal/session (port = 27183 + scid*31 % 100). Because the hash collides
// (e.g. scid 0 and scid 100 both map to port 27183), two actors must not pick
// scids whose hashes collide, or their ADB forwards will fight over the same
// local port. The allocator hands out scids guaranteed to map to distinct
// ports within this daemon process.
//
// scid → port mapping is intentionally mirrored here (not imported) so the
// daemon package has no dependency cycle and the rule stays in one visible
// place. If session changes its hash, update both.
func scidPort(scid int) int {
	h := scid * 31
	if h < 0 {
		h = -h
	}
	return 27183 + h%100
}

// defaultScid is the scrcpy default, used for the first device so a single
// device behaves identically to stock scrcpy (port 27210).
const defaultScid = 0x3f

// ScidAllocator assigns scrcpy scids to device actors so their forwarded TCP
// ports never collide. It is safe for concurrent use.
type ScidAllocator struct {
	mu       sync.Mutex
	usedPort map[int]bool // video ports currently in use
	usedScid map[int]bool // scids currently in use
}

// NewScidAllocator returns an empty allocator.
func NewScidAllocator() *ScidAllocator {
	return &ScidAllocator{
		usedPort: make(map[int]bool),
		usedScid: make(map[int]bool),
	}
}

// Alloc picks a scid whose video port is not in use, reserves it, and returns
// the scid. The first allocation always returns defaultScid (scrcpy's default
// scid 0x3f, which maps to port 27236 via the session hash) so single-device
// setups behave identically to before; later allocations scan upward from
// defaultScid for the next non-colliding scid.
//
// On success the caller owns the scid until Release is called.
func (a *ScidAllocator) Alloc() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// First device: prefer the scrcpy default so single-device usage is
	// indistinguishable from running scrcpy directly.
	if len(a.usedScid) == 0 {
		port := scidPort(defaultScid)
		a.usedScid[defaultScid] = true
		a.usedPort[port] = true
		return defaultScid, nil
	}

	// Subsequent devices: scan scid values above the default for one whose
	// port is free. Start above defaultScid to avoid re-issuing it.
	for scid := defaultScid + 1; scid < defaultScid+256; scid++ {
		if a.usedScid[scid] {
			continue
		}
		port := scidPort(scid)
		if a.usedPort[port] {
			continue // hash collision — same port as an existing device
		}
		a.usedScid[scid] = true
		a.usedPort[port] = true
		phonelog.Default().Write("scid allocator: assigned scid=%#x port=%d", scid, port)
		return scid, nil
	}
	return 0, fmt.Errorf("scid allocator exhausted: no free port in 256-slot window")
}

// Release frees a scid (and its port) for reuse. Called when a device actor
// is permanently removed.
func (a *ScidAllocator) Release(scid int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	port := scidPort(scid)
	delete(a.usedScid, scid)
	delete(a.usedPort, port)
}
