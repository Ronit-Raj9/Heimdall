package visual

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strconv"
	"strings"
)

// Defaults of the checks when a spec sets no threshold.
const (
	DefaultPixelThreshold  = 0.0  // share of differing pixels allowed
	DefaultSSIMThreshold   = 0.98 // lowest mean SSIM
	DefaultPHashThreshold  = 6    // largest Hamming distance of 64 bits
	DefaultColorThreshold  = 24   // largest RGB distance to a dominant colour
	DefaultColorMinShare   = 0.01 // smallest share of the area an expected colour covers
	DefaultRegionThreshold = 0.02 // largest mean absolute difference per channel, 0 to 1
	DefaultRegionOffset    = 2    // largest distance in pixels from the expected position
	ssimWindow             = 8
	ssimStride             = 4
	dominantColors         = 8
)

// PixelDiff is the share of unmasked pixels of area (both images must be the
// same size) whose largest channel difference is over tol, and the count
// compared.
func PixelDiff(a, b *Image, area Rect, mask *Mask, tol int) (float64, int, error) {
	if err := sameSize(a, b); err != nil {
		return 0, 0, err
	}
	var diff, n int
	for y := area.Y; y < area.Y+area.H; y++ {
		for x := area.X; x < area.X+area.W; x++ {
			if mask.Masked(x, y) {
				continue
			}
			n++
			if channelDiff(a, b, x, y) > tol {
				diff++
			}
		}
	}
	if n == 0 {
		return 0, 0, errors.New("every pixel of the area is masked")
	}
	return float64(diff) / float64(n), n, nil
}

// channelDiff is the largest absolute channel difference at (x, y).
func channelDiff(a, b *Image, x, y int) int {
	i := (y*a.W + x) * 3
	d := 0
	for c := 0; c < 3; c++ {
		v := int(a.Pix[i+c]) - int(b.Pix[i+c])
		if v < 0 {
			v = -v
		}
		d = max(d, v)
	}
	return d
}

// SSIM is the mean structural similarity of the luma of area over 8x8
// windows at a stride of 4 (Wang et al. 2004 with uniform window weights,
// C1 = (0.01*255)^2, C2 = (0.03*255)^2). A window that touches a masked
// pixel is left out; it returns the windows used.
func SSIM(a, b *Image, area Rect, mask *Mask) (float64, int, error) {
	if err := sameSize(a, b); err != nil {
		return 0, 0, err
	}
	if area.W < ssimWindow || area.H < ssimWindow {
		return 0, 0, fmt.Errorf("area %dx%d is smaller than the %dx%d SSIM window", area.W, area.H, ssimWindow, ssimWindow)
	}
	la, lb := luma(a), luma(b)
	const c1, c2 = (0.01 * 255) * (0.01 * 255), (0.03 * 255) * (0.03 * 255)
	var sum float64
	var n int
	for y := area.Y; y+ssimWindow <= area.Y+area.H; y += ssimStride {
		for x := area.X; x+ssimWindow <= area.X+area.W; x += ssimStride {
			if windowMasked(mask, x, y) {
				continue
			}
			var sa, sb, saa, sbb, sab float64
			for dy := 0; dy < ssimWindow; dy++ {
				row := (y+dy)*a.W + x
				for dx := 0; dx < ssimWindow; dx++ {
					va, vb := la[row+dx], lb[row+dx]
					sa += va
					sb += vb
					saa += va * va
					sbb += vb * vb
					sab += va * vb
				}
			}
			const k = ssimWindow * ssimWindow
			ma, mb := sa/k, sb/k
			va := saa/k - ma*ma
			vb := sbb/k - mb*mb
			cov := sab/k - ma*mb
			sum += ((2*ma*mb + c1) * (2*cov + c2)) / ((ma*ma + mb*mb + c1) * (va + vb + c2))
			n++
		}
	}
	if n == 0 {
		return 0, 0, errors.New("every SSIM window of the area touches a mask")
	}
	return sum / float64(n), n, nil
}

func windowMasked(mask *Mask, x, y int) bool {
	if mask.Count() == 0 {
		return false
	}
	for dy := 0; dy < ssimWindow; dy++ {
		for dx := 0; dx < ssimWindow; dx++ {
			if mask.Masked(x+dx, y+dy) {
				return true
			}
		}
	}
	return false
}

// luma is Rec. 601 luma of every pixel.
func luma(m *Image) []float64 {
	out := make([]float64, m.W*m.H)
	for i := range out {
		out[i] = 0.299*float64(m.Pix[i*3]) + 0.587*float64(m.Pix[i*3+1]) + 0.114*float64(m.Pix[i*3+2])
	}
	return out
}

// PHash is the 64-bit DCT perceptual hash of area: the luma reduced to
// 32x32 by box averaging, a 2D DCT-II, and one bit per coefficient of the
// low 8x8 block that is above the block's median (the DC term left out of
// the median). Masked pixels count as mid grey, so the same mask on two
// images hides the same content.
func PHash(m *Image, area Rect, mask *Mask) uint64 {
	const n = 32
	var small [n][n]float64
	for j := 0; j < n; j++ {
		y0, y1 := area.Y+j*area.H/n, area.Y+(j+1)*area.H/n
		y1 = max(y1, y0+1)
		for i := 0; i < n; i++ {
			x0, x1 := area.X+i*area.W/n, area.X+(i+1)*area.W/n
			x1 = max(x1, x0+1)
			var s float64
			var c int
			for y := y0; y < y1 && y < m.H; y++ {
				for x := x0; x < x1 && x < m.W; x++ {
					if mask.Masked(x, y) {
						s += 128
					} else {
						r, g, b := m.At(x, y)
						s += 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
					}
					c++
				}
			}
			if c > 0 {
				small[j][i] = s / float64(c)
			}
		}
	}
	var dct [8][8]float64
	for v := 0; v < 8; v++ {
		for u := 0; u < 8; u++ {
			var s float64
			for y := 0; y < n; y++ {
				cy := math.Cos(float64(2*y+1) * float64(v) * math.Pi / (2 * n))
				for x := 0; x < n; x++ {
					s += small[y][x] * math.Cos(float64(2*x+1)*float64(u)*math.Pi/(2*n)) * cy
				}
			}
			dct[v][u] = s
		}
	}
	vals := make([]float64, 0, 63)
	for v := 0; v < 8; v++ {
		for u := 0; u < 8; u++ {
			if u != 0 || v != 0 {
				vals = append(vals, dct[v][u])
			}
		}
	}
	sort.Float64s(vals)
	median := vals[len(vals)/2]
	var h uint64
	for v := 0; v < 8; v++ {
		for u := 0; u < 8; u++ {
			if dct[v][u] > median {
				h |= 1 << uint(v*8+u)
			}
		}
	}
	return h
}

// Hamming is the number of differing bits.
func Hamming(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// Color is one dominant colour of an area and the share of its unmasked
// pixels it covers.
type Color struct {
	RGB   [3]uint8
	Share float64
}

// Hex is the colour as #rrggbb.
func (c Color) Hex() string { return fmt.Sprintf("#%02x%02x%02x", c.RGB[0], c.RGB[1], c.RGB[2]) }

// DominantColors buckets the unmasked pixels of area by their top 5 bits
// per channel and returns the k largest buckets, each as the mean colour of
// its pixels, largest share first (ties by colour, so the order is stable).
func DominantColors(m *Image, area Rect, mask *Mask, k int) []Color {
	type bucket struct {
		n          int
		sr, sg, sb int
	}
	buckets := map[int]*bucket{}
	total := 0
	for y := area.Y; y < area.Y+area.H; y++ {
		for x := area.X; x < area.X+area.W; x++ {
			if mask.Masked(x, y) {
				continue
			}
			r, g, b := m.At(x, y)
			key := int(r>>3)<<10 | int(g>>3)<<5 | int(b>>3)
			bk := buckets[key]
			if bk == nil {
				bk = &bucket{}
				buckets[key] = bk
			}
			bk.n++
			bk.sr += int(r)
			bk.sg += int(g)
			bk.sb += int(b)
			total++
		}
	}
	out := make([]Color, 0, len(buckets))
	for _, bk := range buckets {
		out = append(out, Color{
			RGB:   [3]uint8{uint8((bk.sr + bk.n/2) / bk.n), uint8((bk.sg + bk.n/2) / bk.n), uint8((bk.sb + bk.n/2) / bk.n)},
			Share: float64(bk.n) / float64(total),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Share != out[j].Share {
			return out[i].Share > out[j].Share
		}
		return out[i].Hex() < out[j].Hex()
	})
	if len(out) > k {
		out = out[:k]
	}
	return out
}

// ParseHex reads #rrggbb (or rrggbb).
func ParseHex(s string) ([3]uint8, error) {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(h) != 6 {
		return [3]uint8{}, fmt.Errorf("colour %q is not #rrggbb", s)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return [3]uint8{}, fmt.Errorf("colour %q is not #rrggbb", s)
	}
	return [3]uint8{uint8(v >> 16), uint8(v >> 8), uint8(v)}, nil
}

// ColorDistance is the Euclidean distance of two colours in RGB.
func ColorDistance(a, b [3]uint8) float64 {
	dr, dg, db := float64(a[0])-float64(b[0]), float64(a[1])-float64(b[1]), float64(a[2])-float64(b[2])
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

// ColorMatch is how close an expected colour came: the nearest dominant
// colour covering at least minShare, its distance (+Inf when none does).
func ColorMatch(want [3]uint8, dom []Color, minShare float64) (Color, float64) {
	best, bestD := Color{}, math.Inf(1)
	for _, c := range dom {
		if c.Share < minShare {
			continue
		}
		if d := ColorDistance(want, c.RGB); d < bestD {
			best, bestD = c, d
		}
	}
	return best, bestD
}

// Match is where a crop was found in an image and how far it was from
// identical there: the mean absolute difference per channel, 0 to 1.
type Match struct {
	X, Y int
	Diff float64
}

// FindRegion finds crop in m within search (the crop's top-left corner
// ranges over positions that keep it inside search). It searches a 4x
// reduced copy first and refines the best candidates at full size; when at
// is set it also measures every position within maxOffset of it at full
// size and returns that best as near. Masked pixels of m are left out.
func FindRegion(m, crop *Image, search Rect, mask *Mask, at *Pt, maxOffset int) (best Match, near *Match, err error) {
	search = search.Intersect(m.Bounds())
	if crop.W > search.W || crop.H > search.H {
		return Match{}, nil, fmt.Errorf("crop %dx%d is larger than the search area %dx%d", crop.W, crop.H, search.W, search.H)
	}
	best = Match{Diff: math.Inf(1)}
	const f = 4
	type cand struct {
		x, y int
		d    float64
	}
	var cands []cand
	coarse := crop.W >= 4*f && crop.H >= 2*f
	var sm, sc *Image
	var sx0, sy0, sx1, sy1 int
	if coarse {
		sm, sc = reduce(m, f), reduce(crop, f)
		sx0, sy0 = (search.X+f-1)/f, (search.Y+f-1)/f
		sx1, sy1 = (search.X+search.W)/f-sc.W, (search.Y+search.H)/f-sc.H
		// A narrow search area can hold full-size positions but no
		// position on the reduced grid: scan it at full size instead.
		coarse = sx0 <= sx1 && sy0 <= sy1
	}
	if coarse {
		for y := sy0; y <= sy1; y++ {
			for x := sx0; x <= sx1; x++ {
				cands = append(cands, cand{x * f, y * f, meanDiff(sm, sc, x, y, nil, math.Inf(1))})
			}
		}
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].d != cands[j].d {
				return cands[i].d < cands[j].d
			}
			if cands[i].y != cands[j].y {
				return cands[i].y < cands[j].y
			}
			return cands[i].x < cands[j].x
		})
		if len(cands) > 8 {
			cands = cands[:8]
		}
		for _, c := range cands {
			for y := c.y - f; y <= c.y+f; y++ {
				for x := c.x - f; x <= c.x+f; x++ {
					best = better(best, m, crop, x, y, search, mask)
				}
			}
		}
	} else {
		for y := search.Y; y+crop.H <= search.Y+search.H; y++ {
			for x := search.X; x+crop.W <= search.X+search.W; x++ {
				best = better(best, m, crop, x, y, search, mask)
			}
		}
	}
	if at != nil {
		nb := Match{Diff: math.Inf(1)}
		for y := at.Y - maxOffset; y <= at.Y+maxOffset; y++ {
			for x := at.X - maxOffset; x <= at.X+maxOffset; x++ {
				if (x-at.X)*(x-at.X)+(y-at.Y)*(y-at.Y) > maxOffset*maxOffset {
					continue
				}
				nb = better(nb, m, crop, x, y, search, mask)
			}
		}
		if !math.IsInf(nb.Diff, 1) {
			near = &nb
			if nb.Diff < best.Diff {
				best = nb
			}
		}
	}
	if math.IsInf(best.Diff, 1) {
		return best, near, errors.New("no position of the crop fits the search area")
	}
	return best, near, nil
}

// Pt is a point in pixels.
type Pt struct{ X, Y int }

// better measures the crop at (x, y) and keeps the better match (the lower
// difference, then the upper-left position, so ties resolve the same way).
func better(cur Match, m, crop *Image, x, y int, search Rect, mask *Mask) Match {
	if x < search.X || y < search.Y || x+crop.W > search.X+search.W || y+crop.H > search.Y+search.H {
		return cur
	}
	d := meanDiff(m, crop, x, y, mask, cur.Diff)
	if d < cur.Diff || (d == cur.Diff && (y < cur.Y || (y == cur.Y && x < cur.X))) {
		return Match{X: x, Y: y, Diff: d}
	}
	return cur
}

// meanDiff is the mean absolute difference per channel, 0 to 1, of crop
// placed at (x, y) in m. It stops early, returning +Inf, once the running
// sum cannot beat limit.
func meanDiff(m, crop *Image, x, y int, mask *Mask, limit float64) float64 {
	var sum, n int
	total := crop.W * crop.H * 3
	stop := math.Inf(1)
	if !math.IsInf(limit, 1) {
		stop = limit * 255 * float64(total)
	}
	for cy := 0; cy < crop.H; cy++ {
		mi := ((y+cy)*m.W + x) * 3
		ci := cy * crop.W * 3
		for cx := 0; cx < crop.W; cx++ {
			if mask.Masked(x+cx, y+cy) {
				continue
			}
			for c := 0; c < 3; c++ {
				v := int(m.Pix[mi+cx*3+c]) - int(crop.Pix[ci+cx*3+c])
				if v < 0 {
					v = -v
				}
				sum += v
			}
			n += 3
		}
		if float64(sum) > stop {
			return math.Inf(1)
		}
	}
	if n == 0 {
		return math.Inf(1)
	}
	return float64(sum) / 255 / float64(n)
}

// reduce shrinks the image by f in each direction by box averaging.
func reduce(m *Image, f int) *Image {
	w, h := m.W/f, m.H/f
	out := &Image{W: w, H: h, Pix: make([]uint8, w*h*3)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var s [3]int
			for dy := 0; dy < f; dy++ {
				for dx := 0; dx < f; dx++ {
					r, g, b := m.At(x*f+dx, y*f+dy)
					s[0] += int(r)
					s[1] += int(g)
					s[2] += int(b)
				}
			}
			for c := 0; c < 3; c++ {
				out.Pix[(y*w+x)*3+c] = uint8(s[c] / (f * f))
			}
		}
	}
	return out
}

func sameSize(a, b *Image) error {
	if a.W != b.W || a.H != b.H {
		return fmt.Errorf("capture is %dx%d, reference %dx%d", a.W, a.H, b.W, b.H)
	}
	return nil
}
