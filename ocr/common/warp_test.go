package common

import (
	"image"
	"math"
	"testing"
)

// Identity: mapping a rect to itself should give the identity homography.
func TestHomographyIdentity(t *testing.T) {
	pts := [4][2]float64{{0, 0}, {10, 0}, {10, 4}, {0, 4}}
	H := getPerspectiveTransform(pts, pts)
	for i, v := range H {
		want := 0.0
		if i == 0 || i == 4 || i == 8 {
			want = 1
		}
		if math.Abs(v-want) > 1e-9 {
			t.Errorf("H[%d]=%v want %v (full %v)", i, v, want, H)
		}
	}
}

// Translation: src rect shifted by (5,5) maps dst→src.
func TestHomographyTranslation(t *testing.T) {
	dst := [4][2]float64{{0, 0}, {10, 0}, {10, 4}, {0, 4}}
	src := [4][2]float64{{5, 5}, {15, 5}, {15, 9}, {5, 9}}
	H := getPerspectiveTransform(dst, src) // dst→src
	// Map dst point (0,0) → should be (5,5).
	w := H[6]*0 + H[7]*0 + 1
	sx := (H[0]*0 + H[1]*0 + H[2]) / w
	sy := (H[3]*0 + H[4]*0 + H[5]) / w
	if math.Abs(sx-5) > 1e-9 || math.Abs(sy-5) > 1e-9 {
		t.Errorf("(0,0)→(%v,%v) want (5,5)", sx, sy)
	}
	// Map dst point (10,4) → should be (15,9).
	w = H[6]*10 + H[7]*4 + 1
	sx = (H[0]*10 + H[1]*4 + H[2]) / w
	sy = (H[3]*10 + H[4]*4 + H[5]) / w
	if math.Abs(sx-15) > 1e-9 || math.Abs(sy-9) > 1e-9 {
		t.Errorf("(10,4)→(%v,%v) want (15,9)", sx, sy)
	}
}

// A 45°-rotated square: the warp should map dst axes back to the rotated src.
func TestHomographyRotated(t *testing.T) {
	s := math.Sqrt2 / 2 * 10 // half-diagonal of a 10-wide square rotated 45°
	dst := [4][2]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	src := [4][2]float64{{s, 0}, {2 * s, s}, {s, 2 * s}, {0, s}}
	H := getPerspectiveTransform(dst, src)
	for _, p := range dst {
		w := H[6]*p[0] + H[7]*p[1] + 1
		sx := (H[0]*p[0] + H[1]*p[1] + H[2]) / w
		sy := (H[3]*p[0] + H[4]*p[1] + H[5]) / w
		// Should map back to the corresponding src corner.
		// Find nearest src corner.
		best := 1e9
		for _, sp := range src {
			d := math.Hypot(sx-sp[0], sy-sp[1])
			if d < best {
				best = d
			}
		}
		if best > 1e-6 {
			t.Errorf("dst %v → (%v,%v), nearest src %v", p, sx, sy, best)
		}
	}
}

// TestRot90CCW verifies numpy rot90(k=1) semantics: a 2×3 image (h=2,w=3)
// [[A B C]
//
//	[D E F]]  →  CCW rot →  (h_out=3, w_out=2)
//
// [[C F]
//
//	[B E]
//	[A D]]
func TestRot90CCW(t *testing.T) {
	// 2×3 image: pixel value = col+1 (so A=1,B=2,C=3,D=1,E=2,F=3 won't be
	// unique). Use unique: value = row*10+col.
	w, h := 3, 2
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(y*10 + x + 1)
			pi := y*src.Stride + x*4
			src.Pix[pi] = v
		}
	}
	// numpy rot90 CCW (verified): out[i,j]=m[j,w-1-i].
	// src row0=[1,2,3] row1=[11,12,13] → out dims (w,h)=(3,2):
	//   out row0 = [src[0][2], src[1][2]] = [3,13]
	//   out row1 = [src[0][1], src[1][1]] = [2,12]
	//   out row2 = [src[0][0], src[1][0]] = [1,11]
	want := [3][2]uint8{{3, 13}, {2, 12}, {1, 11}}
	dst := rot90CCW(src)
	if dst.Bounds().Dx() != h || dst.Bounds().Dy() != w {
		t.Fatalf("dims: got %dx%d want %dx%d", dst.Bounds().Dx(), dst.Bounds().Dy(), h, w)
	}
	for i := 0; i < w; i++ {
		for j := 0; j < h; j++ {
			got := dst.Pix[i*dst.Stride+j*4]
			if got != want[i][j] {
				t.Errorf("out[%d][%d]=%d want %d", i, j, got, want[i][j])
			}
		}
	}
}
