package voice

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"strings"
)

// The tone code in Go: the same codec as tonecode.py (its docstring has the
// layout), so the fault proxy's scripted model can speak and transcribe it
// at $0 and Go tests can build call fixtures. Audio is 16-bit little-endian
// mono PCM.

// Tone code constants, equal to tonecode.py's.
const (
	SampleRate    = 16000
	FrameMS       = 20
	symbolSamples = SampleRate * FrameMS / 1000
	startHz       = 2700.0
	endHz         = 2900.0
	maxTextBytes  = 1200
	toneAmplitude = 9000
	toneFade      = 24
)

func dataHz(n int) float64 { return 800 + 100*float64(n) }

func crc8(data []byte) byte {
	var crc byte
	for _, b := range data {
		crc ^= b
		for i := 0; i < 8; i++ {
			if crc&0x80 != 0 {
				crc = crc<<1 ^ 0x07
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func appendTone(out []int16, freq float64) []int16 {
	w := 2 * math.Pi * freq / SampleRate
	for i := 0; i < symbolSamples; i++ {
		g := 1.0
		if i < toneFade {
			g = float64(i) / toneFade
		} else if i >= symbolSamples-toneFade {
			g = float64(symbolSamples-1-i) / toneFade
		}
		// Truncation toward zero, as Python's int() does, so both encoders give the same bytes.
		out = append(out, int16(toneAmplitude*g*math.Sin(w*float64(i))))
	}
	return out
}

// ToneEncode returns text as tone-coded PCM with leadMS and tailMS of
// silence around it, byte for byte what tonecode.encode returns.
func ToneEncode(text string, leadMS, tailMS int) []byte {
	data := []byte(text)
	if len(data) > maxTextBytes {
		data = data[:maxTextBytes]
	}
	nibbles := []int{len(data) >> 8 & 0xF, len(data) >> 4 & 0xF, len(data) & 0xF}
	for _, b := range data {
		nibbles = append(nibbles, int(b>>4), int(b&0xF))
	}
	c := crc8(data)
	nibbles = append(nibbles, int(c>>4), int(c&0xF))
	var s []int16
	s = append(s, make([]int16, SampleRate*leadMS/1000)...)
	s = appendTone(s, startHz)
	s = appendTone(s, startHz)
	for _, n := range nibbles {
		s = appendTone(s, dataHz(n))
	}
	s = appendTone(s, endHz)
	s = appendTone(s, endHz)
	s = append(s, make([]int16, SampleRate*tailMS/1000)...)
	return samplesToBytes(s)
}

func samplesToBytes(s []int16) []byte {
	out := make([]byte, 2*len(s))
	for i, v := range s {
		binary.LittleEndian.PutUint16(out[2*i:], uint16(v))
	}
	return out
}

func bytesToSamples(pcm []byte) []float64 {
	n := len(pcm) / 2
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = float64(int16(binary.LittleEndian.Uint16(pcm[2*i:])))
	}
	return out
}

func goertzel(x []float64, start, n int, freq float64) float64 {
	coeff := 2 * math.Cos(2*math.Pi*freq/SampleRate)
	var s1, s2 float64
	for _, v := range x[start : start+n] {
		s0 := v + coeff*s1 - s2
		s2, s1 = s1, s0
	}
	return s1*s1 + s2*s2 - coeff*s1*s2
}

func energy(x []float64, start, n int) float64 {
	var e float64
	for _, v := range x[start : start+n] {
		e += v * v
	}
	return e
}

func toneRatio(x []float64, start, n int, freq float64) float64 {
	if start+n > len(x) {
		return 0
	}
	e := energy(x, start, n)
	if e < float64(n)*400 {
		return 0
	}
	return goertzel(x, start, n, freq) / (e * float64(n) / 2)
}

var allHz = func() []float64 {
	out := make([]float64, 0, 18)
	for n := 0; n < 16; n++ {
		out = append(out, dataHz(n))
	}
	return append(out, startHz, endHz)
}()

func symbolAt(x []float64, start int) (float64, bool) {
	s, n := start+40, symbolSamples-80
	if s+n > len(x) {
		return 0, false
	}
	if energy(x, s, n) < float64(n)*400 {
		return 0, false
	}
	best, bestP := 0.0, -1.0
	for _, f := range allHz {
		if p := goertzel(x, s, n, f); p > bestP {
			best, bestP = f, p
		}
	}
	return best, true
}

func findStart(x []float64, from int) int {
	for i := from; i+symbolSamples <= len(x); i += 80 {
		if toneRatio(x, i, symbolSamples/2, startHz) > 0.5 {
			j := i - symbolSamples/2
			if j < from {
				j = from
			}
			for j < i && toneRatio(x, j, symbolSamples/2, startHz) < 0.5 {
				j += 8
			}
			return j
		}
	}
	return -1
}

var errNoCode = errors.New("no tone code")

func decodeAt(x []float64, pos int) (string, int, error) {
	k := pos
	starts := 0
	for starts < 3 {
		f, ok := symbolAt(x, k)
		if !ok || f != startHz {
			break
		}
		starts++
		k += symbolSamples
	}
	if starts == 0 {
		return "", pos + symbolSamples, errors.New("no start symbol")
	}
	var nibbles []int
	want := -1
	for {
		f, ok := symbolAt(x, k)
		if !ok {
			return "", k, errors.New("audio ended inside the code")
		}
		k += symbolSamples
		if f == endHz {
			break
		}
		if f == startHz {
			return "", k, errors.New("start symbol inside the code")
		}
		nibbles = append(nibbles, int(math.Round((f-800)/100)))
		if len(nibbles) == 3 {
			want = nibbles[0]<<8 | nibbles[1]<<4 | nibbles[2]
			if want > maxTextBytes {
				return "", k, errors.New("length out of range")
			}
		}
		if want >= 0 && len(nibbles) == 3+2*want+2 {
			if f2, ok := symbolAt(x, k); ok && f2 == endHz {
				k += symbolSamples
			}
			break
		}
	}
	if want < 0 || len(nibbles) != 3+2*want+2 {
		return "", k, errors.New("length mismatch")
	}
	body := nibbles[3 : 3+2*want]
	data := make([]byte, want)
	for i := 0; i < want; i++ {
		data[i] = byte(body[2*i]<<4 | body[2*i+1])
	}
	if crc8(data) != byte(nibbles[len(nibbles)-2]<<4|nibbles[len(nibbles)-1]) {
		return "", k, errors.New("checksum mismatch")
	}
	if f, ok := symbolAt(x, k); ok && f == endHz {
		k += symbolSamples
	}
	return string(data), k, nil
}

// ToneDecode returns every tone-coded text in pcm joined by spaces; ok is
// false when none decodes.
func ToneDecode(pcm []byte) (string, bool) {
	x := bytesToSamples(pcm)
	var texts []string
	for i := 0; ; {
		p := findStart(x, i)
		if p < 0 {
			break
		}
		text, end, err := decodeAt(x, p)
		if err == nil {
			texts = append(texts, text)
		}
		if end < p+symbolSamples {
			end = p + symbolSamples
		}
		i = end
	}
	if len(texts) == 0 {
		return "", false
	}
	return strings.Join(texts, " "), true
}

// WAV returns pcm (16-bit mono at rate) as a WAV file.
func WAV(pcm []byte, rate int) []byte {
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + len(pcm)))
	b.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1))
	w(uint16(1))
	w(uint32(rate))
	w(uint32(rate * 2))
	w(uint16(2))
	w(uint16(16))
	b.WriteString("data")
	w(uint32(len(pcm)))
	b.Write(pcm)
	return b.Bytes()
}

// ReadWAV returns the PCM and sample rate of a 16-bit mono WAV file (a
// stereo file is mixed down).
func ReadWAV(data []byte) ([]byte, int, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, errors.New("not a WAV file")
	}
	var rate, channels, bits int
	var pcm []byte
	for off := 12; off+8 <= len(data); {
		id := string(data[off : off+4])
		size := int(binary.LittleEndian.Uint32(data[off+4:]))
		body := off + 8
		if size < 0 || body+size > len(data) {
			size = len(data) - body
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, 0, errors.New("short fmt chunk")
			}
			channels = int(binary.LittleEndian.Uint16(data[body+2:]))
			rate = int(binary.LittleEndian.Uint32(data[body+4:]))
			bits = int(binary.LittleEndian.Uint16(data[body+14:]))
		case "data":
			pcm = data[body : body+size]
		}
		off = body + size + size%2
	}
	if bits != 16 || rate == 0 || pcm == nil {
		return nil, 0, errors.New("want 16-bit PCM WAV")
	}
	if channels == 2 {
		mono := make([]byte, 0, len(pcm)/2)
		for i := 0; i+3 < len(pcm); i += 4 {
			l := int(int16(binary.LittleEndian.Uint16(pcm[i:])))
			r := int(int16(binary.LittleEndian.Uint16(pcm[i+2:])))
			mono = binary.LittleEndian.AppendUint16(mono, uint16(int16((l+r)/2)))
		}
		pcm = mono
	} else if channels != 1 {
		return nil, 0, errors.New("want mono or stereo WAV")
	}
	return pcm, rate, nil
}

// Resample converts 16-bit PCM between rates by linear interpolation, as
// tonecode.resample does.
func Resample(pcm []byte, from, to int) []byte {
	if from == to || len(pcm) < 2 {
		return pcm
	}
	x := bytesToSamples(pcm)
	n := int(float64(len(x)) * float64(to) / float64(from))
	out := make([]int16, n)
	step := float64(from) / float64(to)
	for i := 0; i < n; i++ {
		p := float64(i) * step
		j := int(p)
		f := p - float64(j)
		a := x[j]
		b := a
		if j+1 < len(x) {
			b = x[j+1]
		}
		out[i] = int16(a + (b-a)*f)
	}
	return samplesToBytes(out)
}
