// Package visual grades what an agent's work looks like (X1 vision grading,
// docs/visual-grading.md): deterministic checks on images the bench
// captured itself (dimensions, pixel difference, SSIM, perceptual hash,
// dominant colours, region presence), the diff heatmap the run page shows,
// the captures themselves (a browser page, a desktop screenshot, an
// artifact rendered in a clean room) and the lint rules of output.visual.
// Only the Go standard library's image packages decode and compare images.
package visual

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
)

// Limits on an image the checks read. A capture over them is refused, not
// cut, so a grade never rests on part of a picture.
const (
	MaxImageBytes  = 16 << 20 // 16 MiB encoded
	MaxImageWidth  = 8192
	MaxImageHeight = 16384
	MaxImagePixels = 40_000_000
)

// Image is a decoded picture as opaque 8-bit RGB: transparent pixels are
// composited over white, so a PNG with an alpha channel and the same
// picture without one compare equal.
type Image struct {
	W, H   int
	Pix    []uint8 // RGB, 3 bytes per pixel, row by row
	Format string  // png | jpeg
}

// At returns the RGB of pixel (x, y).
func (m *Image) At(x, y int) (uint8, uint8, uint8) {
	i := (y*m.W + x) * 3
	return m.Pix[i], m.Pix[i+1], m.Pix[i+2]
}

// Bounds is the whole image as a rectangle.
func (m *Image) Bounds() Rect { return Rect{0, 0, m.W, m.H} }

// Rect is a rectangle in pixels (top-left X, Y; width W, height H).
type Rect struct{ X, Y, W, H int }

// Empty reports whether r covers no pixel.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Inside reports whether r lies within an image of w x h pixels.
func (r Rect) Inside(w, h int) bool {
	return r.X >= 0 && r.Y >= 0 && r.W > 0 && r.H > 0 && r.X+r.W <= w && r.Y+r.H <= h
}

// Intersect is the part of r inside o.
func (r Rect) Intersect(o Rect) Rect {
	x0, y0 := max(r.X, o.X), max(r.Y, o.Y)
	x1, y1 := min(r.X+r.W, o.X+o.W), min(r.Y+r.H, o.Y+o.H)
	if x1 <= x0 || y1 <= y0 {
		return Rect{}
	}
	return Rect{x0, y0, x1 - x0, y1 - y0}
}

// Decode reads a PNG or JPEG of at most MaxImageBytes, checks its size
// against the limits before decoding the pixels, and flattens it to RGB.
func Decode(data []byte) (*Image, error) {
	if len(data) == 0 {
		return nil, errors.New("image is empty")
	}
	if len(data) > MaxImageBytes {
		return nil, fmt.Errorf("image is %d bytes, over the %d byte cap", len(data), MaxImageBytes)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("image is not a png or jpeg: %w", err)
	}
	if format != "png" && format != "jpeg" {
		return nil, fmt.Errorf("image format %s is not png or jpeg", format)
	}
	if err := checkSize(cfg.Width, cfg.Height); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", format, err)
	}
	m := FromImage(src)
	m.Format = format
	return m, nil
}

// DecodeReader is Decode over a reader, reading at most MaxImageBytes+1.
func DecodeReader(r io.Reader) (*Image, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxImageBytes+1))
	if err != nil {
		return nil, err
	}
	return Decode(data)
}

func checkSize(w, h int) error {
	switch {
	case w <= 0 || h <= 0:
		return fmt.Errorf("image is %dx%d", w, h)
	case w > MaxImageWidth || h > MaxImageHeight:
		return fmt.Errorf("image is %dx%d, over the %dx%d cap", w, h, MaxImageWidth, MaxImageHeight)
	case w*h > MaxImagePixels:
		return fmt.Errorf("image is %dx%d, over the %d pixel cap", w, h, MaxImagePixels)
	}
	return nil
}

// FromImage flattens any image to opaque RGB over white.
func FromImage(src image.Image) *Image {
	b := src.Bounds()
	nrgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(nrgba, nrgba.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(nrgba, nrgba.Bounds(), src, b.Min, draw.Over)
	m := &Image{W: b.Dx(), H: b.Dy(), Pix: make([]uint8, b.Dx()*b.Dy()*3)}
	for y := 0; y < m.H; y++ {
		row := nrgba.Pix[y*nrgba.Stride : y*nrgba.Stride+m.W*4]
		for x := 0; x < m.W; x++ {
			copy(m.Pix[(y*m.W+x)*3:], row[x*4:x*4+3])
		}
	}
	return m
}

// NewImage is a w x h image filled with one colour.
func NewImage(w, h int, c [3]uint8) *Image {
	m := &Image{W: w, H: h, Pix: make([]uint8, w*h*3), Format: "png"}
	for i := 0; i < w*h; i++ {
		copy(m.Pix[i*3:], c[:])
	}
	return m
}

// Fill paints r (clipped to the image) with one colour.
func (m *Image) Fill(r Rect, c [3]uint8) {
	r = r.Intersect(m.Bounds())
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			copy(m.Pix[(y*m.W+x)*3:], c[:])
		}
	}
}

// Crop copies r out of the image; r must be inside it.
func (m *Image) Crop(r Rect) *Image {
	out := &Image{W: r.W, H: r.H, Pix: make([]uint8, r.W*r.H*3), Format: m.Format}
	for y := 0; y < r.H; y++ {
		copy(out.Pix[y*r.W*3:(y+1)*r.W*3], m.Pix[((r.Y+y)*m.W+r.X)*3:((r.Y+y)*m.W+r.X+r.W)*3])
	}
	return out
}

// EncodePNG writes the image as a PNG with default compression, which is
// deterministic for the same pixels.
func (m *Image) EncodePNG() ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, m.W, m.H))
	for i := 0; i < m.W*m.H; i++ {
		copy(img.Pix[i*4:], m.Pix[i*3:i*3+3])
		img.Pix[i*4+3] = 0xff
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodeJPEG is for tests: a lossy copy of the image.
func (m *Image) encodeJPEG(quality int) ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, m.W, m.H))
	for i := 0; i < m.W*m.H; i++ {
		copy(img.Pix[i*4:], m.Pix[i*3:i*3+3])
		img.Pix[i*4+3] = 0xff
	}
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	return buf.Bytes(), err
}

// Mask marks the pixels every comparison ignores.
type Mask struct {
	W, H int
	bits []bool
	n    int
}

// NewMask builds the mask of rects (clipped to w x h).
func NewMask(w, h int, rects []Rect) *Mask {
	m := &Mask{W: w, H: h, bits: make([]bool, w*h)}
	for _, r := range rects {
		r = r.Intersect(Rect{0, 0, w, h})
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				if !m.bits[y*w+x] {
					m.bits[y*w+x] = true
					m.n++
				}
			}
		}
	}
	return m
}

// Masked reports whether pixel (x, y) is ignored. A nil mask ignores none.
func (m *Mask) Masked(x, y int) bool {
	return m != nil && m.bits[y*m.W+x]
}

// Count is the number of masked pixels.
func (m *Mask) Count() int {
	if m == nil {
		return 0
	}
	return m.n
}
