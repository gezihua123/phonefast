package postprocess

import (
	"math"
	"testing"
)

// TestMinAreaRect recovers a known rotated rectangle's min-area bounding box.
// A 100×30 rectangle rotated 45° about the origin: its 4 corners, fed to
// MinAreaRect, must yield width≈100, height≈30 (the true min area 3000).
func TestMinAreaRect(t *testing.T) {
	// 4 corners of a 100×30 rect rotated 45° around origin, plus interior
	// points (minAreaRect must ignore interiors — only hull extremes matter).
	w, h := 100.0, 30.0
	s := math.Sqrt2 / 2
	corners := [][2]float64{
		{(w/2)*s - (h/2)*s, (w/2)*s + (h/2)*s},
		{(w/2)*s + (h/2)*s, (w/2)*s - (h/2)*s},
		{-(w/2)*s + (h/2)*s, -(w/2)*s - (h/2)*s},
		{-(w/2)*s - (h/2)*s, -(w/2)*s + (h/2)*s},
	}
	// Add some interior points to prove they don't affect the result.
	pts := append([][2]float64{}, corners...)
	pts = append(pts, [2]float64{0, 0}, [2]float64{10, 10}, [2]float64{-5, 5})

	got, gw, gh := MinAreaRect(pts)
	gotArea := gw * gh
	if math.Abs(gotArea-w*h) > 1.0 {
		t.Errorf("min area: got %.2f want %.2f (w=%.2f h=%.2f)", gotArea, w*h, gw, gh)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 corners, got %d", len(got))
	}
	// The min-area rect of a rectangle is the rectangle itself, so the 4
	// returned corners must equal the 4 input corners (as a set, within
	// tolerance). Interior points (0,0),(10,10),(-5,5) must NOT appear.
	matched := 0
	for _, gc := range got {
		for _, ic := range corners {
			if math.Abs(gc[0]-ic[0]) < 1e-6 && math.Abs(gc[1]-ic[1]) < 1e-6 {
				matched++
				break
			}
		}
	}
	if matched != 4 {
		t.Errorf("returned corners %v do not match input corners %v (matched %d/4)", got, corners, matched)
	}
}

// TestGetMiniBoxesWinding verifies the [TL, TR, BR, BL] winding from
// processors.py:439-453 on an axis-aligned box (winding is unambiguous there).
func TestGetMiniBoxesWinding(t *testing.T) {
	pts := [][2]float64{{0, 0}, {10, 0}, {10, 4}, {0, 4}, {5, 2}}
	box, sside := GetMiniBoxes(pts)
	if sside < 3.9 || sside > 4.1 {
		t.Errorf("sside: got %.2f want ~4", sside)
	}
	// TL=(0,0), TR=(10,0), BR=(10,4), BL=(0,4) — y increases downward.
	want := [4][2]float64{{0, 0}, {10, 0}, {10, 4}, {0, 4}}
	for i := 0; i < 4; i++ {
		if math.Abs(box[i][0]-want[i][0]) > 1e-9 || math.Abs(box[i][1]-want[i][1]) > 1e-9 {
			t.Errorf("corner %d: got %v want %v (full %v)", i, box[i], want[i], box)
		}
	}
}

// pointInQuad removed — the corner-set match above is a stronger, exact test.
