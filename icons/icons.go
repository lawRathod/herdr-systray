// Package icons generates 24x24 PNG tray icons programmatically.
// Idle: green filled circle.
// Working: 4-frame pulsing amber circle.
// Blocked: static red circle.
// Done: green circle with checkmark.
// Unknown: gray circle with question mark.
package icons

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

const size = 24

var (
	colGreen  = color.RGBA{0x22, 0x88, 0x22, 0xff}
	colAmber  = color.RGBA{0xdd, 0x99, 0x22, 0xff}
	colRed    = color.RGBA{0xcc, 0x33, 0x33, 0xff}
	colGray   = color.RGBA{0x88, 0x88, 0x88, 0xff}
	colWhite  = color.RGBA{0xff, 0xff, 0xff, 0xff}
)

// State icons.

func Idle() []byte     { return pngEncode(drawFilledCircleAt(0.42, colGreen)) }
func Blocked() []byte  { return pngEncode(drawStopSign()) }
func Done() []byte     { return pngEncode(drawCheckmark()) }
func Unknown() []byte  { return pngEncode(drawQuestionMark()) }

// WorkingFrames returns 4 PNG frames for a pulsing "breathing" circle.
func WorkingFrames() [4][]byte {
	radii := [4]float64{0.20, 0.32, 0.44, 0.32}
	var frames [4][]byte
	for i, r := range radii {
		frames[i] = pngEncode(drawFilledCircleAt(r, colAmber))
	}
	return frames
}

// ---------------------------------------------------------------------------
// drawing helpers
// ---------------------------------------------------------------------------

func drawFilledCircleAt(rf float64, fill color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.Transparent, image.Point{}, draw.Src)
	cx, cy := float64(size)/2, float64(size)/2
	r := float64(size) * rf
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			samples := 0
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					fx := float64(x) + float64(dx)*0.5
					fy := float64(y) + float64(dy)*0.5
					dxc, dyc := fx-cx, fy-cy
					if dxc*dxc+dyc*dyc <= r*r {
						samples++
					}
				}
			}
			if samples >= 2 {
				img.Set(x, y, fill)
			}
		}
	}
	return img
}

// drawStopSign draws a red octagon with inner white circle (not a full
// octagon since we're at 24x24 — just a red circle with border).
func drawStopSign() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.Transparent, image.Point{}, draw.Src)
	cx, cy := float64(size)/2, float64(size)/2
	outerR := float64(size) * 0.42
	// inner white circle
	innerR := outerR * 0.55
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var outer, inner bool
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					fx := float64(x) + float64(dx)*0.5
					fy := float64(y) + float64(dy)*0.5
					dxc, dyc := fx-cx, fy-cy
					d2 := dxc*dxc + dyc*dyc
					if d2 <= outerR*outerR {
						outer = true
					}
					if d2 <= innerR*innerR {
						inner = true
					}
				}
			}
			if inner {
				// White inner circle — leave transparent or draw white
				// leave transparent so the red ring is the stop sign
			} else if outer {
				// at least 2 of 4 outer samples
				s := 0
				for dy := 0; dy < 2; dy++ {
					for dx := 0; dx < 2; dx++ {
						fx := float64(x) + float64(dx)*0.5
						fy := float64(y) + float64(dy)*0.5
						dxc, dyc := fx-cx, fy-cy
						if dxc*dxc+dyc*dyc <= outerR*outerR {
							s++
						}
					}
				}
				if s >= 2 {
					img.Set(x, y, colRed)
				}
				// draw inner over the center to punch a hole
				innerS := 0
				for dy := 0; dy < 2; dy++ {
					for dx := 0; dx < 2; dx++ {
						fx := float64(x) + float64(dx)*0.5
						fy := float64(y) + float64(dy)*0.5
						dxc, dyc := fx-cx, fy-cy
						if dxc*dxc+dyc*dyc <= innerR*innerR {
							innerS++
						}
					}
				}
				if innerS >= 2 {
					img.Set(x, y, colWhite)
				}
			}
		}
	}
	return img
}

// drawCheckmark draws a green circle with a white checkmark.
func drawCheckmark() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.Transparent, image.Point{}, draw.Src)
	cx, cy := float64(size)/2, float64(size)/2
	r := float64(size) * 0.42
	// Green circle
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			samples := 0
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					fx := float64(x) + float64(dx)*0.5
					fy := float64(y) + float64(dy)*0.5
					dxc, dyc := fx-cx, fy-cy
					if dxc*dxc+dyc*dyc <= r*r {
						samples++
					}
				}
			}
			if samples >= 2 {
				img.Set(x, y, colGreen)
			}
		}
	}
	// White checkmark — approximate at 24x24: two stroke lines
	// Simplified as a filled checkmark shape
	// Checkmark points: (7,13) → (11,17) → (18,7) in 24x24 coords
	checkPoints := [][2]float64{
		{7, 13}, {8, 14}, {9, 15}, {10, 16}, {11, 17},
		{12, 16}, {13, 15}, {14, 14}, {15, 13}, {16, 12},
		{17, 11}, {18, 10}, {18, 9}, {17, 8}, {18, 7},
		{17, 7}, {16, 8}, {11, 13}, {10, 12}, {9, 11},
		{8, 10}, {7, 9},
	}
	for _, p := range checkPoints {
		if p[0] >= 0 && p[0] < size && p[1] >= 0 && p[1] < size {
			img.Set(int(p[0]), int(p[1]), colWhite)
		}
	}
	// Also do anti-aliased interpolation for neighbouring pixels
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			c := img.RGBAAt(x, y)
			if c == colWhite {
				continue
			}
			// Check if pixel is adjacent to a white pixel for antialiasing
			adj := false
			for dy := -1; dy <= 1 && !adj; dy++ {
				for dx := -1; dx <= 1 && !adj; dx++ {
					nx, ny := x+dx, y+dy
					if nx >= 0 && nx < size && ny >= 0 && ny < size {
						if img.RGBAAt(nx, ny) == colWhite {
							adj = true
						}
					}
				}
			}
			if adj {
				samples := 0
				for dy := 0; dy < 2; dy++ {
					for dx := 0; dx < 2; dx++ {
						fx := float64(x) + float64(dx)*0.5
						fy := float64(y) + float64(dy)*0.5
						// Check if near any checkmark line
						for _, p := range checkPoints {
							dxc := fx - p[0]
							dyc := fy - p[1]
							if dxc*dxc+dyc*dyc <= 1.5 {
								samples++
								break
							}
						}
					}
				}
				if samples >= 1 {
					img.Set(x, y, colWhite)
				}
			}
		}
	}
	return img
}

// drawQuestionMark draws a gray circle with a white question mark.
func drawQuestionMark() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.Transparent, image.Point{}, draw.Src)
	cx, cy := float64(size)/2, float64(size)/2
	r := float64(size) * 0.42
	// Gray circle
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			samples := 0
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					fx := float64(x) + float64(dx)*0.5
					fy := float64(y) + float64(dy)*0.5
					dxc, dyc := fx-cx, fy-cy
					if dxc*dxc+dyc*dyc <= r*r {
						samples++
					}
				}
			}
			if samples >= 2 {
				img.Set(x, y, colGray)
			}
		}
	}
	// White question mark — approximate pixel blob
	qmarks := [][2]int{
		{10, 7}, {11, 7}, {12, 7}, {13, 7},
		{14, 8}, {14, 9}, {13, 10}, {12, 11}, {11, 12},
		{11, 13}, {11, 14}, {11, 15},
		{10, 17}, {11, 17}, {12, 17},
	}
	for _, p := range qmarks {
		if p[0] >= 0 && p[0] < size && p[1] >= 0 && p[1] < size {
			img.Set(p[0], p[1], colWhite)
		}
	}
	return img
}

func pngEncode(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
