package visual

// Heatmap draws where the capture differs from the reference: the capture
// in faded grey, every pixel whose largest channel difference is over tol in
// red (brighter for a larger difference), and masked pixels tinted blue. It
// returns nil when the two images differ in size, and the number of
// differing unmasked pixels.
func Heatmap(capture, reference *Image, mask *Mask, tol int) (*Image, int) {
	if capture.W != reference.W || capture.H != reference.H {
		return nil, 0
	}
	out := &Image{W: capture.W, H: capture.H, Pix: make([]uint8, len(capture.Pix)), Format: "png"}
	diff := 0
	for y := 0; y < capture.H; y++ {
		for x := 0; x < capture.W; x++ {
			i := (y*capture.W + x) * 3
			r, g, b := capture.At(x, y)
			grey := uint8(200 + (int(r)*299+int(g)*587+int(b)*114)/1000*55/255)
			px := [3]uint8{grey, grey, grey}
			switch d := channelDiff(capture, reference, x, y); {
			case mask.Masked(x, y):
				px = [3]uint8{grey / 2, grey / 2, 255}
			case d > tol:
				diff++
				px = [3]uint8{uint8(155 + d*100/255), 0, 0}
			}
			copy(out.Pix[i:], px[:])
		}
	}
	return out, diff
}
