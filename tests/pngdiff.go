// pngdiff compares two PNGs pixel-by-pixel: dimensions, differing pixel
// count, and max per-channel delta. Used to validate encoder-path changes
// produce equivalent output on a static screen.
// Usage: go run tests/pngdiff.go a.png b.png
package main

import (
	"fmt"
	"image/png"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: pngdiff a.png b.png")
		os.Exit(2)
	}
	decode := func(p string) ([][]uint8, int, int) {
		f, err := os.Open(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		img, err := png.Decode(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		rows := make([][]uint8, h)
		for y := 0; y < h; y++ {
			rows[y] = make([]uint8, w*4)
			for x := 0; x < w; x++ {
				r, g, b_, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				rows[y][x*4+0] = uint8(r >> 8)
				rows[y][x*4+1] = uint8(g >> 8)
				rows[y][x*4+2] = uint8(b_ >> 8)
				rows[y][x*4+3] = uint8(a >> 8)
			}
		}
		return rows, w, h
	}
	a, wa, ha := decode(os.Args[1])
	b, wb, hb := decode(os.Args[2])
	fmt.Printf("A: %dx%d  B: %dx%d\n", wa, ha, wb, hb)
	if wa != wb || ha != hb {
		fmt.Println("DIMENSION MISMATCH")
		os.Exit(1)
	}
	diffPx, maxDelta, sum := 0, 0, 0
	for y := 0; y < ha; y++ {
		for x := 0; x < wa*4; x++ {
			d := int(a[y][x]) - int(b[y][x])
			if d < 0 {
				d = -d
			}
			if d > 0 {
				diffPx++
				sum += d
				if d > maxDelta {
					maxDelta = d
				}
			}
		}
	}
	total := wa * ha * 4
	fmt.Printf("diff channels: %d / %d (%.4f%%)  maxDelta=%d  meanDelta=%.4f\n",
		diffPx, total, float64(diffPx)/float64(total)*100, maxDelta,
		func() float64 { if diffPx == 0 { return 0 }; return float64(sum) / float64(diffPx) }())
}
