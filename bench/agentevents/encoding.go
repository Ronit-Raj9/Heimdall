package agentevents

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"strings"
)

// ErrEncoding is returned for a content coding the collector does not read.
var ErrEncoding = errors.New("content encoding not supported")

// ErrDecodedTooLarge is returned when a compressed body expands past the
// limit.
var ErrDecodedTooLarge = errors.New("decoded body too large")

// DecodeContent undoes the Content-Encoding of a request body. An empty or
// identity coding returns raw unchanged; gzip, x-gzip and deflate are
// decoded, reading at most limit decoded bytes (a small compressed body can
// expand without bound); any other coding returns ErrEncoding.
func DecodeContent(raw []byte, encoding string, limit int64) ([]byte, error) {
	var rd io.Reader
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
		return raw, nil
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		rd = zr
	case "deflate":
		// RFC 9110 deflate is zlib; some clients send raw deflate.
		if zr, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
			rd = zr
		} else {
			rd = flate.NewReader(bytes.NewReader(raw))
		}
	default:
		return nil, ErrEncoding
	}
	b, err := io.ReadAll(io.LimitReader(rd, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) == limit {
		var one [1]byte
		if n, _ := io.ReadFull(rd, one[:]); n > 0 {
			return nil, ErrDecodedTooLarge
		}
	}
	return b, nil
}
