package visual

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

var (
	white = [3]uint8{255, 255, 255}
	blue  = [3]uint8{0x1d, 0x4e, 0xd8}
	red   = [3]uint8{0xdc, 0x26, 0x26}
	grey  = [3]uint8{0x6b, 0x72, 0x80}
)

// scene is a 200x120 page: a gradient band at the top, a button at (bx, by)
// and a bar of text-like stripes, so SSIM and the hash have structure.
func scene(bx, by int, button [3]uint8) *Image {
	m := NewImage(200, 120, white)
	for x := 0; x < 200; x++ {
		v := uint8(120 + x/2)
		m.Fill(Rect{x, 0, 1, 12}, [3]uint8{v, v, v})
	}
	m.Fill(Rect{bx, by, 60, 24}, button)
	for i := 0; i < 8; i++ {
		m.Fill(Rect{20 + i*20, 90, 12, 6}, grey)
	}
	return m
}

func noisy(m *Image, seed int64, amp int) *Image {
	out := &Image{W: m.W, H: m.H, Pix: append([]uint8(nil), m.Pix...), Format: m.Format}
	r := rand.New(rand.NewSource(seed))
	for i := range out.Pix {
		v := int(out.Pix[i]) + r.Intn(2*amp+1) - amp
		out.Pix[i] = uint8(min(max(v, 0), 255))
	}
	return out
}

func TestPixelDiffIdenticalShiftColourAndTolerance(t *testing.T) {
	ref := scene(70, 40, blue)
	if v, n, err := PixelDiff(scene(70, 40, blue), ref, ref.Bounds(), nil, 0); err != nil || v != 0 || n != 200*120 {
		t.Fatalf("identical: %v %d %v", v, n, err)
	}
	shifted, _, _ := PixelDiff(scene(72, 40, blue), ref, ref.Bounds(), nil, 0)
	if want := float64(2*24*2) / (200 * 120); shifted != want {
		t.Fatalf("shift by 2 px: %v, want %v", shifted, want)
	}
	recolour, _, _ := PixelDiff(scene(70, 40, red), ref, ref.Bounds(), nil, 0)
	if want := float64(60*24) / (200 * 120); recolour != want {
		t.Fatalf("recolour: %v, want %v", recolour, want)
	}
	n := noisy(ref, 1, 3)
	if v, _, _ := PixelDiff(n, ref, ref.Bounds(), nil, 3); v != 0 {
		t.Fatalf("noise of 3 within tolerance 3: %v", v)
	}
	if v, _, _ := PixelDiff(n, ref, ref.Bounds(), nil, 0); v < 0.5 {
		t.Fatalf("noise of 3 at tolerance 0: %v, want most pixels", v)
	}
	if _, _, err := PixelDiff(NewImage(10, 10, white), ref, ref.Bounds(), nil, 0); err == nil || !strings.Contains(err.Error(), "10x10") {
		t.Fatalf("size mismatch: %v", err)
	}
}

func TestMasksHideTheDifference(t *testing.T) {
	ref := scene(70, 40, blue)
	got := scene(70, 40, red)
	mask := NewMask(200, 120, []Rect{{70, 40, 60, 24}})
	if v, n, _ := PixelDiff(got, ref, ref.Bounds(), mask, 0); v != 0 || n != 200*120-60*24 {
		t.Fatalf("masked recolour: %v over %d", v, n)
	}
	if v, _, _ := SSIM(got, ref, ref.Bounds(), mask); v != 1 {
		t.Fatalf("masked SSIM: %v", v)
	}
	if PHash(got, ref.Bounds(), mask) != PHash(ref, ref.Bounds(), mask) {
		t.Fatal("masked hashes differ")
	}
	all := NewMask(200, 120, []Rect{{0, 0, 200, 120}})
	if _, _, err := PixelDiff(got, ref, ref.Bounds(), all, 0); err == nil {
		t.Fatal("fully masked pixel diff should refuse")
	}
}

func TestSSIMOrdersDamage(t *testing.T) {
	ref := scene(70, 40, blue)
	same, _, _ := SSIM(scene(70, 40, blue), ref, ref.Bounds(), nil)
	light, _, _ := SSIM(noisy(ref, 2, 2), ref, ref.Bounds(), nil)
	heavy, _, _ := SSIM(noisy(ref, 3, 40), ref, ref.Bounds(), nil)
	moved, _, _ := SSIM(scene(20, 60, blue), ref, ref.Bounds(), nil)
	if same != 1 {
		t.Fatalf("identical SSIM %v", same)
	}
	if !(light > 0.9 && light < 1) || !(heavy < light) || !(moved < 0.98) {
		t.Fatalf("light %v heavy %v moved %v", light, heavy, moved)
	}
}

func TestPHashIsStableUnderNoiseAndJPEG(t *testing.T) {
	ref := scene(70, 40, blue)
	h := PHash(ref, ref.Bounds(), nil)
	if d := Hamming(h, PHash(noisy(ref, 4, 4), ref.Bounds(), nil)); d > 4 {
		t.Fatalf("noise moved the hash by %d bits", d)
	}
	jp, err := ref.encodeJPEG(80)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := Decode(jp)
	if err != nil || dec.Format != "jpeg" {
		t.Fatalf("decode jpeg: %v", err)
	}
	if d := Hamming(h, PHash(dec, ref.Bounds(), nil)); d > 6 {
		t.Fatalf("jpeg moved the hash by %d bits", d)
	}
	if d := Hamming(h, PHash(scene(10, 80, red), ref.Bounds(), nil)); d <= 6 {
		t.Fatalf("a different layout is only %d bits away", d)
	}
}

func TestDominantColors(t *testing.T) {
	m := NewImage(100, 100, white)
	m.Fill(Rect{0, 0, 100, 30}, blue)
	m.Fill(Rect{0, 30, 100, 10}, red)
	dom := DominantColors(m, m.Bounds(), nil, 8)
	if len(dom) != 3 || dom[0].Hex() != "#ffffff" || dom[1].Hex() != "#1d4ed8" || dom[2].Hex() != "#dc2626" {
		t.Fatalf("dominant %+v", dom)
	}
	if dom[1].Share != 0.3 {
		t.Fatalf("blue share %v", dom[1].Share)
	}
	want, _ := ParseHex("#1e4fd9")
	if c, d := ColorMatch(want, dom, 0.01); c.Hex() != "#1d4ed8" || d > 2 {
		t.Fatalf("match %v %v", c, d)
	}
	if c, d := ColorMatch(want, dom, 0.5); c.Hex() != "#ffffff" || d < 200 {
		t.Fatalf("min share not applied: %v %v", c, d)
	}
	if _, d := ColorMatch(want, dom, 0.9); d < 1000 {
		t.Fatalf("no colour covers 0.9: %v", d)
	}
	if _, err := ParseHex("blue"); err == nil {
		t.Fatal("ParseHex accepted a name")
	}
}

func TestFindRegionLocatesAShiftedCrop(t *testing.T) {
	ref := scene(70, 40, blue)
	crop := ref.Crop(Rect{66, 36, 68, 32})
	got := scene(100, 60, blue)
	best, near, err := FindRegion(got, crop, got.Bounds(), nil, &Pt{66, 36}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if best.X != 96 || best.Y != 56 || best.Diff != 0 {
		t.Fatalf("best %+v", best)
	}
	if near == nil || near.Diff == 0 {
		t.Fatalf("near %+v should not match", near)
	}
	best, near, _ = FindRegion(ref, crop, ref.Bounds(), nil, &Pt{65, 37}, 2)
	if near == nil || near.X != 66 || near.Y != 36 || near.Diff != 0 || best != *near {
		t.Fatalf("within offset: best %+v near %+v", best, near)
	}
	other := scene(100, 60, red)
	best, _, _ = FindRegion(other, crop, other.Bounds(), nil, nil, 0)
	if best.Diff < 0.05 {
		t.Fatalf("recoloured button matched with %v", best.Diff)
	}
	small := ref.Crop(Rect{70, 40, 6, 6})
	if b, _, err := FindRegion(ref, small, Rect{60, 30, 30, 30}, nil, nil, 0); err != nil || b.Diff != 0 {
		t.Fatalf("small crop: %+v %v", b, err)
	}
}

func TestEvaluateRunsEveryCheck(t *testing.T) {
	ref := scene(70, 40, blue)
	spec := &schemas.VisualCheck{
		Method: schemas.VisualMethodSSIM, Threshold: 0.99,
		Masks: []schemas.VisualRect{{X: 0, Y: 0, W: 200, H: 12}},
		Checks: []schemas.VisualAssert{
			{Method: schemas.VisualMethodDimensions},
			{Method: schemas.VisualMethodPixel, Tolerance: 8},
			{Method: schemas.VisualMethodPHash},
			{ID: "button_blue", Method: schemas.VisualMethodColors, Colors: []string{"#1d4ed8"}, Area: &schemas.VisualRect{X: 60, Y: 30, W: 80, H: 44}},
			{ID: "button_centred", Method: schemas.VisualMethodRegion, Crop: &schemas.VisualRect{X: 66, Y: 36, W: 68, H: 32}, At: &schemas.VisualPoint{X: 66, Y: 36}},
		},
	}
	ids := []string{"ssim", "dimensions_1", "pixel_2", "phash_3", "button_blue", "button_centred"}
	pass := Evaluate(spec, Inputs{Capture: noisy(ref, 5, 2), Reference: ref})
	for i, r := range pass {
		if r.ID != ids[i] || !r.Passed {
			t.Fatalf("check %d: %+v", i, r)
		}
	}
	if n, s := Score(pass); n != 6 || *s != 1 {
		t.Fatalf("score %d %v", n, *s)
	}
	moved := Evaluate(spec, Inputs{Capture: scene(20, 40, blue), Reference: ref})
	if moved[5].Passed || !strings.Contains(moved[5].Detail, "found at (16,36), not at (66,36)") {
		t.Fatalf("moved region: %+v", moved[5])
	}
	if !moved[4].Passed {
		t.Fatalf("moved colours should still pass: %+v", moved[4])
	}
	recoloured := Evaluate(spec, Inputs{Capture: scene(70, 40, red), Reference: ref})
	if recoloured[4].Passed || recoloured[5].Passed || recoloured[1].Passed != true {
		t.Fatalf("recoloured: %+v", recoloured)
	}
	if n, s := Score(recoloured); n == 6 || *s == 1 {
		t.Fatalf("recoloured score %d %v", n, *s)
	}
	noRef := Evaluate(spec, Inputs{Capture: ref})
	if noRef[0].Passed || noRef[0].Detail != "no reference image" {
		t.Fatalf("no reference: %+v", noRef[0])
	}
}

func TestEvaluateIsDeterministic(t *testing.T) {
	ref := scene(70, 40, blue)
	spec := &schemas.VisualCheck{Method: schemas.VisualMethodSSIM, Checks: []schemas.VisualAssert{{Method: schemas.VisualMethodPHash}, {Method: schemas.VisualMethodColors, Colors: []string{"#1d4ed8"}}}}
	capture := noisy(ref, 9, 6)
	first := Evaluate(spec, Inputs{Capture: capture, Reference: ref})
	for i := 0; i < 4; i++ {
		again := Evaluate(spec, Inputs{Capture: capture, Reference: ref})
		for j := range first {
			if first[j].Value != again[j].Value || first[j].Detail != again[j].Detail {
				t.Fatalf("run %d check %d: %+v vs %+v", i, j, first[j], again[j])
			}
		}
	}
}

func TestDecodeFlattensAlphaAndRefusesBadInput(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.NRGBA{R: 0, G: 0, B: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	m, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if r, g, b := m.At(0, 0); r != 255 || g != 255 || b != 255 {
		t.Fatalf("transparent pixel is %d,%d,%d, want white", r, g, b)
	}
	if r, g, b := m.At(1, 1); r != 0 || g != 0 || b != 255 {
		t.Fatalf("opaque pixel is %d,%d,%d", r, g, b)
	}
	if _, err := Decode([]byte("<svg/>")); err == nil {
		t.Fatal("svg decoded")
	}
	if _, err := Decode(nil); err == nil {
		t.Fatal("empty decoded")
	}
	huge := image.NewGray(image.Rect(0, 0, 9000, 2))
	buf.Reset()
	if err := png.Encode(&buf, huge); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("oversized: %v", err)
	}
	enc, err := m.EncodePNG()
	if err != nil {
		t.Fatal(err)
	}
	enc2, _ := m.EncodePNG()
	if !bytes.Equal(enc, enc2) {
		t.Fatal("png encoding is not deterministic")
	}
}

func TestHeatmap(t *testing.T) {
	ref := scene(70, 40, blue)
	got := scene(70, 40, red)
	mask := NewMask(200, 120, []Rect{{0, 0, 10, 10}})
	hm, n := Heatmap(got, ref, mask, 0)
	if hm == nil || n != 60*24 {
		t.Fatalf("heatmap diff %d", n)
	}
	if r, g, b := hm.At(80, 50); r < 155 || g != 0 || b != 0 {
		t.Fatalf("diff pixel %d,%d,%d", r, g, b)
	}
	if _, _, b := hm.At(5, 5); b != 255 {
		t.Fatal("mask not tinted")
	}
	if hm, _ := Heatmap(NewImage(3, 3, white), ref, nil, 0); hm != nil {
		t.Fatal("size mismatch heatmap")
	}
}

// TestFindRegionNarrowSearchOffGrid covers QC2-24-05: a search area that
// fits the crop at full size but holds no position of the reduced grid is
// scanned at full size, not refused.
func TestFindRegionNarrowSearchOffGrid(t *testing.T) {
	m := NewImage(24, 16, [3]uint8{0, 0, 0})
	crop := NewImage(16, 8, [3]uint8{0, 0, 0})
	for _, origin := range []Pt{{0, 0}, {1, 1}, {3, 5}} {
		b, _, err := FindRegion(m, crop, Rect{origin.X, origin.Y, 16, 8}, nil, nil, 0)
		if err != nil || b.Diff != 0 || b.X != origin.X || b.Y != origin.Y {
			t.Fatalf("origin %v: %+v %v", origin, b, err)
		}
	}
}
