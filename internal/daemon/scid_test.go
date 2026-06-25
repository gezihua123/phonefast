package daemon

import "testing"

func TestScidPortMatchesSessionHash(t *testing.T) {
	// The daemon's scidPort must agree with session.hashScid so an actor's
	// allocated port matches what session.Connect actually opens.
	// Both use port = 27183 + (scid*31 % 100); defaultScid (0x3f=63) → 27236.
	scid := defaultScid
	want := 27183 + (scid*31)%100 // 27236
	got := scidPort(scid)
	if got != want {
		t.Fatalf("scidPort(%#x) = %d, want %d", scid, got, want)
	}
}

func TestScidAllocatorFirstIsDefault(t *testing.T) {
	a := NewScidAllocator()
	s, err := a.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	if s != defaultScid {
		t.Fatalf("first alloc = %#x, want default %#x", s, defaultScid)
	}
}

func TestScidAllocatorNoPortCollision(t *testing.T) {
	a := NewScidAllocator()
	seen := map[int]int{} // port → scid that claimed it
	for i := 0; i < 20; i++ {
		s, err := a.Alloc()
		if err != nil {
			t.Fatalf("alloc %d: %v", i, err)
		}
		port := scidPort(s)
		if other, dup := seen[port]; dup {
			t.Fatalf("port %d collision: scid %#x reuses port claimed by scid %#x", port, s, other)
		}
		seen[port] = s
	}
	if len(seen) != 20 {
		t.Fatalf("expected 20 distinct ports, got %d", len(seen))
	}
}

func TestScidAllocatorReleaseReuses(t *testing.T) {
	a := NewScidAllocator()
	first, _ := a.Alloc() // defaultScid
	a.Release(first)
	if a.usedScid[first] {
		t.Fatal("scid not released")
	}
	again, err := a.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Fatalf("after release, alloc = %#x, want reused %#x", again, first)
	}
}
