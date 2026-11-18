"""Heimdall tone code and audio helpers, standard library only.

Part of Heimdall (bench/voice). The tone code is a deterministic test codec
that carries text inside audio: every 20 ms symbol is one sine tone, so a
sentence takes about as long to play as to say (25 bytes a second). It is
what the $0 scripted audio fakes speak and what the admission controls send,
so a call can be graded without a speech model and gives the same transcript
every time. It is not speech: a real agent's speech-to-text cannot read it,
and a result measured with tone-coded audio says nothing about how an agent
copes with real voices (docs/voice-agents.md).

Layout of one burst, one symbol per 20 ms (320 samples at 16 kHz):
  START START  len(3 nibbles)  data nibbles (high, low)  crc8(2 nibbles)  END END
Data nibble n is a tone at 800 + 100 n Hz; START is 2700 Hz, END 2900 Hz.

Audio everywhere in the voice track is 16-bit little-endian signed PCM, mono,
16 kHz (pcm_s16le), sent in 20 ms frames of 640 bytes.
"""

import array
import io
import math
import random
import struct
import sys
import wave

SAMPLE_RATE = 16000
FRAME_MS = 20
FRAME_SAMPLES = SAMPLE_RATE * FRAME_MS // 1000
FRAME_BYTES = FRAME_SAMPLES * 2
SYMBOL = FRAME_SAMPLES
START_HZ, END_HZ = 2700.0, 2900.0
MAX_TEXT_BYTES = 1200
AMPLITUDE = 9000
FADE = 24


def data_hz(n):
    return 800.0 + 100.0 * n


def crc8(data):
    crc = 0
    for b in data:
        crc ^= b
        for _ in range(8):
            crc = ((crc << 1) ^ 0x07) & 0xFF if crc & 0x80 else (crc << 1) & 0xFF
    return crc


def _tone(freq, n=SYMBOL, amp=AMPLITUDE):
    out = array.array("h", bytes(2 * n))
    w = 2 * math.pi * freq / SAMPLE_RATE
    for i in range(n):
        g = 1.0
        if i < FADE:
            g = i / FADE
        elif i >= n - FADE:
            g = (n - 1 - i) / FADE
        out[i] = int(amp * g * math.sin(w * i))
    return out


_TONES = {}


def tone(freq):
    t = _TONES.get(freq)
    if t is None:
        t = _tone(freq)
        _TONES[freq] = t
    return t


def encode(text, lead_ms=0, tail_ms=0):
    """Text as tone-coded PCM bytes, with optional silence around it."""
    data = str(text).encode("utf-8")[:MAX_TEXT_BYTES]
    nibbles = [(len(data) >> 8) & 0xF, (len(data) >> 4) & 0xF, len(data) & 0xF]
    for b in data:
        nibbles += [b >> 4, b & 0xF]
    c = crc8(data)
    nibbles += [c >> 4, c & 0xF]
    out = array.array("h")
    out.extend(silence_samples(lead_ms))
    out.extend(tone(START_HZ))
    out.extend(tone(START_HZ))
    for n in nibbles:
        out.extend(tone(data_hz(n)))
    out.extend(tone(END_HZ))
    out.extend(tone(END_HZ))
    out.extend(silence_samples(tail_ms))
    return to_bytes(out)


def silence_samples(ms):
    return array.array("h", bytes(2 * (SAMPLE_RATE * int(ms) // 1000)))


def silence(ms):
    return bytes(2 * (SAMPLE_RATE * int(ms) // 1000))


def to_samples(pcm):
    a = array.array("h")
    a.frombytes(bytes(pcm[: len(pcm) - (len(pcm) % 2)]))
    if sys.byteorder == "big":
        a.byteswap()
    return a


def to_bytes(samples):
    a = array.array("h", samples)
    if sys.byteorder == "big":
        a.byteswap()
    return a.tobytes()


def goertzel(x, start, n, freq):
    coeff = 2.0 * math.cos(2.0 * math.pi * freq / SAMPLE_RATE)
    s1 = s2 = 0.0
    for v in x[start:start + n]:
        s0 = v + coeff * s1 - s2
        s2 = s1
        s1 = s0
    return s1 * s1 + s2 * s2 - coeff * s1 * s2


def energy(x, start, n):
    return float(sum(v * v for v in x[start:start + n]))


def ratio(x, start, n, freq):
    """Share of a window's energy at freq, about 1 for a clean tone."""
    e = energy(x, start, n)
    if e < n * 400.0:  # quieter than about -38 dBFS: no tone here
        return 0.0
    return goertzel(x, start, n, freq) / (e * n / 2.0)


ALL_HZ = [data_hz(n) for n in range(16)] + [START_HZ, END_HZ]


def symbol_at(x, start):
    """The strongest code tone in the centre of the symbol window at start."""
    s, n = start + 40, SYMBOL - 80
    if s + n > len(x):
        return None, 0.0
    e = energy(x, s, n)
    if e < n * 400.0:
        return None, 0.0
    best, best_p = None, -1.0
    for f in ALL_HZ:
        p = goertzel(x, s, n, f)
        if p > best_p:
            best, best_p = f, p
    return best, best_p / (e * n / 2.0)


def find_start(x, frm=0):
    """Sample index where a START tone begins, or -1."""
    step = 80
    i = frm
    while i + SYMBOL <= len(x):
        if ratio(x, i, SYMBOL // 2, START_HZ) > 0.5:
            # Back off to the onset: the first offset whose window is START.
            j = max(frm, i - SYMBOL // 2)
            while j < i and ratio(x, j, SYMBOL // 2, START_HZ) < 0.5:
                j += 8
            return j
        i += step
    return -1


def decode_at(x, pos):
    """Decode one burst whose START begins at pos: (text or None, end index, reason)."""
    nibbles = []
    k = pos
    # Skip the START symbols.
    starts = 0
    while starts < 3:
        f, conf = symbol_at(x, k)
        if f != START_HZ:
            break
        starts += 1
        k += SYMBOL
    if starts == 0:
        return None, pos + SYMBOL, "no start symbol"
    want = None
    while True:
        f, conf = symbol_at(x, k)
        if f is None:
            return None, k, "audio ended inside the code"
        k += SYMBOL
        if f == END_HZ:
            break
        if f == START_HZ:
            return None, k, "start symbol inside the code"
        nibbles.append(int(round((f - 800.0) / 100.0)))
        if len(nibbles) == 3:
            want = (nibbles[0] << 8) | (nibbles[1] << 4) | nibbles[2]
            if want > MAX_TEXT_BYTES:
                return None, k, "length out of range"
        if want is not None and len(nibbles) == 3 + 2 * want + 2:
            f2, _ = symbol_at(x, k)
            if f2 == END_HZ:
                k += SYMBOL
            break
    if want is None or len(nibbles) != 3 + 2 * want + 2:
        return None, k, "length mismatch"
    body = nibbles[3:3 + 2 * want]
    data = bytes((body[i] << 4) | body[i + 1] for i in range(0, len(body), 2))
    c = (nibbles[-2] << 4) | nibbles[-1]
    if crc8(data) != c:
        return None, k, "checksum mismatch"
    # Skip a trailing END symbol.
    f, _ = symbol_at(x, k)
    if f == END_HZ:
        k += SYMBOL
    return data.decode("utf-8", "replace"), k, ""


def decode_all(pcm):
    """Every tone-coded text in pcm: (texts, problems). Empty when there is no code."""
    x = to_samples(pcm)
    texts, problems = [], []
    i = 0
    while True:
        p = find_start(x, i)
        if p < 0:
            break
        text, end, why = decode_at(x, p)
        if text is None:
            problems.append(why)
        else:
            texts.append(text)
        i = max(end, p + SYMBOL)
    return texts, problems


def decode(pcm):
    """The tone-coded text in pcm joined by spaces, or None when none decodes."""
    texts, _ = decode_all(pcm)
    return " ".join(texts) if texts else None


def rms(pcm):
    x = to_samples(pcm)
    if not x:
        return 0.0
    return math.sqrt(sum(v * v for v in x) / len(x))


def frames(pcm, size=FRAME_BYTES):
    """pcm split into frames of size bytes, the last padded with silence."""
    pcm = bytes(pcm)
    out = []
    for i in range(0, len(pcm), size):
        f = pcm[i:i + size]
        if len(f) < size:
            f += bytes(size - len(f))
        out.append(f)
    return out


def resample(pcm, src_rate, dst_rate):
    """Linear resampling, for 16 kHz to 24 kHz (OpenAI Realtime) and back, and for speed probes."""
    if src_rate == dst_rate:
        return bytes(pcm)
    x = to_samples(pcm)
    if not x:
        return b""
    n = int(len(x) * dst_rate / src_rate)
    out = array.array("h", bytes(2 * n))
    step = src_rate / dst_rate
    for i in range(n):
        p = i * step
        j = int(p)
        f = p - j
        a = x[j]
        b = x[j + 1] if j + 1 < len(x) else a
        out[i] = int(a + (b - a) * f)
    return to_bytes(out)


def change_speed(pcm, factor):
    """Play pcm factor times faster (pitch rises with it, as on a tape)."""
    if factor <= 0:
        raise ValueError("speed factor must be positive")
    return resample(pcm, int(SAMPLE_RATE * factor), SAMPLE_RATE)


def add_noise(pcm, snr_db, seed):
    """pcm with white noise at snr_db below its own level, seeded so a probe is reproducible."""
    x = to_samples(pcm)
    if not x:
        return b""
    level = math.sqrt(sum(v * v for v in x) / len(x)) or 300.0
    sigma = level / (10 ** (snr_db / 20.0))
    rnd = random.Random(seed)
    out = array.array("h", bytes(2 * len(x)))
    for i, v in enumerate(x):
        out[i] = max(-32768, min(32767, int(v + rnd.gauss(0.0, sigma))))
    return to_bytes(out)


def wav_bytes(pcm, rate=SAMPLE_RATE):
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(bytes(pcm))
    return buf.getvalue()


def read_wav(data):
    """(pcm bytes, sample rate) from WAV bytes; mono 16-bit only, stereo is mixed down."""
    with wave.open(io.BytesIO(data), "rb") as w:
        ch, width, rate = w.getnchannels(), w.getsampwidth(), w.getframerate()
        raw = w.readframes(w.getnframes())
    if width != 2:
        raise ValueError(f"wav sample width {width * 8} bits: want 16")
    if ch == 2:
        x = to_samples(raw)
        raw = to_bytes(array.array("h", [(x[i] + x[i + 1]) // 2 for i in range(0, len(x) - 1, 2)]))
    elif ch != 1:
        raise ValueError(f"wav with {ch} channels: want mono")
    return raw, rate


def to_16k(data):
    """Audio bytes (WAV, or raw 16 kHz pcm) as 16 kHz pcm."""
    if data[:4] == b"RIFF":
        pcm, rate = read_wav(data)
        return resample(pcm, rate, SAMPLE_RATE)
    return bytes(data)


def wav_header_ok(data):
    return len(data) > 44 and data[:4] == b"RIFF" and data[8:12] == b"WAVE"


def pack_u32(n):
    return struct.pack("<I", n)
