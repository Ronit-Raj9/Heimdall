package visual

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Inputs are what the checks of one grade read: the capture, the hidden
// reference (nil when the task names none) and the task folder, where crop
// files are read from.
type Inputs struct {
	Capture   *Image
	Reference *Image
	TaskDir   string
}

// Checks lists a spec's checks in grade order: the first check (Method and
// Threshold over the whole image) and then Checks, each with its id.
func Checks(v *schemas.VisualCheck) []schemas.VisualAssert {
	var out []schemas.VisualAssert
	if v == nil {
		return nil
	}
	if v.Method != "" {
		out = append(out, schemas.VisualAssert{ID: v.Method, Method: v.Method, Threshold: v.Threshold, Tolerance: v.Tolerance})
	}
	for i, c := range v.Checks {
		if c.ID == "" {
			c.ID = c.Method + "_" + strconv.Itoa(i+1)
		}
		out = append(out, c)
	}
	return out
}

// Evaluate runs every check of v on in and returns one result per check,
// in Checks order. A check that cannot be measured (a reference missing, a
// size mismatch for a pixel comparison) fails with the reason in Detail:
// the capture is the agent's work, so its shortfall is the agent's.
func Evaluate(v *schemas.VisualCheck, in Inputs) []schemas.VisualCheckResult {
	var masks []Rect
	for _, r := range v.Masks {
		masks = append(masks, rect(r))
	}
	mask := NewMask(in.Capture.W, in.Capture.H, masks)
	var out []schemas.VisualCheckResult
	for _, c := range Checks(v) {
		out = append(out, evaluateOne(c, in, mask))
	}
	return out
}

func evaluateOne(c schemas.VisualAssert, in Inputs, mask *Mask) schemas.VisualCheckResult {
	res := schemas.VisualCheckResult{ID: c.ID, Method: c.Method, Threshold: Threshold(c)}
	area := in.Capture.Bounds()
	if c.Area != nil {
		area = rect(*c.Area)
		res.Area = c.Area
		if !area.Inside(in.Capture.W, in.Capture.H) {
			res.Detail = fmt.Sprintf("area %s is not inside the %dx%d capture", rectText(area), in.Capture.W, in.Capture.H)
			return res
		}
	}
	needRef := c.Method == schemas.VisualMethodPixel || c.Method == schemas.VisualMethodSSIM || c.Method == schemas.VisualMethodPHash ||
		(c.Method == schemas.VisualMethodRegion && c.CropFile == "") ||
		(c.Method == schemas.VisualMethodDimensions && (c.Width == 0 || c.Height == 0))
	if needRef && in.Reference == nil {
		res.Detail = "no reference image"
		return res
	}
	switch c.Method {
	case schemas.VisualMethodDimensions:
		w, h := c.Width, c.Height
		if w == 0 || h == 0 {
			w, h = in.Reference.W, in.Reference.H
		}
		res.Threshold = 1
		res.Passed = in.Capture.W == w && in.Capture.H == h
		res.Value = b2f(res.Passed)
		res.Detail = fmt.Sprintf("capture %dx%d, expected %dx%d", in.Capture.W, in.Capture.H, w, h)
	case schemas.VisualMethodPixel:
		v, n, err := PixelDiff(in.Capture, in.Reference, area, mask, c.Tolerance)
		if err != nil {
			res.Detail = err.Error()
			return res
		}
		res.Value = round(v)
		res.Passed = v <= res.Threshold
		res.Detail = fmt.Sprintf("%d of %d pixels differ by more than %d in a channel", int(math.Round(v*float64(n))), n, c.Tolerance)
	case schemas.VisualMethodSSIM:
		v, n, err := SSIM(in.Capture, in.Reference, area, mask)
		if err != nil {
			res.Detail = err.Error()
			return res
		}
		res.Value = round(v)
		res.Passed = v >= res.Threshold
		res.Detail = fmt.Sprintf("mean SSIM over %d windows", n)
	case schemas.VisualMethodPHash:
		ref := in.Reference
		if ref.W != in.Capture.W || ref.H != in.Capture.H {
			res.Detail = fmt.Sprintf("capture is %dx%d, reference %dx%d", in.Capture.W, in.Capture.H, ref.W, ref.H)
			return res
		}
		a, b := PHash(in.Capture, area, mask), PHash(ref, area, mask)
		d := Hamming(a, b)
		res.Value = float64(d)
		res.Passed = float64(d) <= res.Threshold
		res.Detail = fmt.Sprintf("hash %016x, reference %016x, %d of 64 bits differ", a, b, d)
	case schemas.VisualMethodColors:
		minShare := c.MinShare
		if minShare == 0 {
			minShare = DefaultColorMinShare
		}
		dom := DominantColors(in.Capture, area, mask, dominantColors)
		worst := 0.0
		var parts []string
		for _, s := range c.Colors {
			want, err := ParseHex(s)
			if err != nil {
				res.Detail = err.Error()
				return res
			}
			got, d := ColorMatch(want, dom, minShare)
			worst = math.Max(worst, d)
			if math.IsInf(d, 1) {
				parts = append(parts, fmt.Sprintf("%s: no dominant colour covers %.3g of the area", strings.ToLower(s), minShare))
			} else {
				parts = append(parts, fmt.Sprintf("%s: nearest %s (share %.3f) at distance %.1f", strings.ToLower(s), got.Hex(), got.Share, d))
			}
		}
		if math.IsInf(worst, 1) {
			worst = 999
		}
		res.Value = round(worst)
		res.Passed = worst <= res.Threshold
		var top []string
		for _, d := range dom {
			top = append(top, fmt.Sprintf("%s %.3f", d.Hex(), d.Share))
		}
		res.Detail = strings.Join(parts, "; ") + "; dominant: " + strings.Join(top, ", ")
	case schemas.VisualMethodRegion:
		evaluateRegion(c, in, area, mask, &res)
	default:
		res.Detail = fmt.Sprintf("method %q is not one of dimensions, pixel, ssim, phash, colors, region", c.Method)
	}
	return res
}

func evaluateRegion(c schemas.VisualAssert, in Inputs, area Rect, mask *Mask, res *schemas.VisualCheckResult) {
	crop, err := regionCrop(c, in)
	if err != nil {
		res.Detail = err.Error()
		return
	}
	off := c.MaxOffset
	if off == 0 {
		off = DefaultRegionOffset
	}
	var at *Pt
	if c.At != nil {
		at = &Pt{c.At.X, c.At.Y}
	}
	best, near, err := FindRegion(in.Capture, crop, area, mask, at, off)
	if err != nil {
		res.Detail = err.Error()
		return
	}
	res.Found = &schemas.VisualPoint{X: best.X, Y: best.Y}
	switch {
	case at == nil:
		res.Value = round(best.Diff)
		res.Passed = best.Diff <= res.Threshold
		res.Detail = fmt.Sprintf("best match of the %dx%d crop at (%d,%d), difference %.4f", crop.W, crop.H, best.X, best.Y, best.Diff)
	case near != nil && near.Diff <= res.Threshold:
		res.Value, res.Passed = round(near.Diff), true
		res.Found = &schemas.VisualPoint{X: near.X, Y: near.Y}
		res.Detail = fmt.Sprintf("crop %dx%d found at (%d,%d), expected (%d,%d) within %d px, difference %.4f", crop.W, crop.H, near.X, near.Y, at.X, at.Y, off, near.Diff)
	case best.Diff <= res.Threshold:
		res.Value = round(best.Diff)
		res.Detail = fmt.Sprintf("crop %dx%d found at (%d,%d), not at (%d,%d) within %d px", crop.W, crop.H, best.X, best.Y, at.X, at.Y, off)
	default:
		res.Value = round(best.Diff)
		if near != nil {
			res.Value = round(near.Diff)
		}
		res.Detail = fmt.Sprintf("crop %dx%d not found: best difference %.4f at (%d,%d), threshold %.4f", crop.W, crop.H, best.Diff, best.X, best.Y, res.Threshold)
	}
}

// regionCrop is the image a region check looks for.
func regionCrop(c schemas.VisualAssert, in Inputs) (*Image, error) {
	if c.CropFile != "" {
		data, err := readTaskFile(in.TaskDir, c.CropFile)
		if err != nil {
			return nil, err
		}
		return Decode(data)
	}
	if c.Crop == nil {
		return nil, errors.New("region check needs crop or crop_file")
	}
	r := rect(*c.Crop)
	if !r.Inside(in.Reference.W, in.Reference.H) {
		return nil, fmt.Errorf("crop %s is not inside the %dx%d reference", rectText(r), in.Reference.W, in.Reference.H)
	}
	return in.Reference.Crop(r), nil
}

// readTaskFile reads a file named relative to the task folder, refusing a
// path that leaves it.
func readTaskFile(dir, rel string) ([]byte, error) {
	p, err := TaskPath(dir, rel)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	return data, nil
}

// TaskPath resolves rel inside the task folder dir; a path that is absolute
// or leaves the folder is refused.
func TaskPath(dir, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(filepath.Clean(rel), "..") {
		return "", fmt.Errorf("path %q must be relative to the task folder and stay inside it", rel)
	}
	return filepath.Join(dir, filepath.Clean(rel)), nil
}

// Threshold is a check's threshold with the method's default applied.
func Threshold(c schemas.VisualAssert) float64 {
	if c.Threshold != 0 {
		return c.Threshold
	}
	switch c.Method {
	case schemas.VisualMethodPixel:
		return DefaultPixelThreshold
	case schemas.VisualMethodSSIM:
		return DefaultSSIMThreshold
	case schemas.VisualMethodPHash:
		return DefaultPHashThreshold
	case schemas.VisualMethodColors:
		return DefaultColorThreshold
	case schemas.VisualMethodRegion:
		return DefaultRegionThreshold
	case schemas.VisualMethodDimensions:
		return 1
	}
	return 0
}

// Score is the share of checks that passed, nil with no checks.
func Score(results []schemas.VisualCheckResult) (int, *float64) {
	if len(results) == 0 {
		return 0, nil
	}
	n := 0
	for _, r := range results {
		if r.Passed {
			n++
		}
	}
	s := round(float64(n) / float64(len(results)))
	return n, &s
}

func rect(r schemas.VisualRect) Rect { return Rect{r.X, r.Y, r.W, r.H} }

func rectText(r Rect) string { return fmt.Sprintf("%dx%d at (%d,%d)", r.W, r.H, r.X, r.Y) }

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// round keeps 6 decimals, so a grade's numbers print and hash the same way
// on every machine.
func round(v float64) float64 { return math.Round(v*1e6) / 1e6 }
