package protocol

// scrcpy's video/control port is derived from the scid via
// ScidVideoPortBase + ScidHash(scid). This formula is THE single owner of the
// scid→port mapping: both the daemon's ScidAllocator (which guarantees
// collision-free ports) and session.Connect (which forwards the port) derive
// from it, so the two can never drift apart. Previously each package kept a
// mirrored copy coupled only by comments.
const ScidVideoPortBase = 27183

// ScidHash computes the port offset for a scrcpy scid: abs(scid*31) % 100.
func ScidHash(scid int) int {
	h := scid * 31
	if h < 0 {
		h = -h
	}
	return h % 100
}

// ScidPort returns the scrcpy video/control TCP port for the given scid.
func ScidPort(scid int) int {
	return ScidVideoPortBase + ScidHash(scid)
}
