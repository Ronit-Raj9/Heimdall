package adapters

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/maximhq/bifrost/bench/schemas"
)

const maxParserLineBytes = 1 << 20

type parsedLine struct {
	recognized bool
	malformed  bool
	emitted    int
}

func scanLines(ctx context.Context, raw []byte, fn func([]byte) parsedLine) (schemas.ParseDiagnostics, error) {
	return scanReader(ctx, bytes.NewReader(raw), fn)
}

// scanReader retains one record (at most 1 MiB of content) and a 64 KiB read buffer.
// Oversized records are drained through their newline, so later lines are
// still parsed. Cancellation and I/O errors return the counts already read.
func scanReader(ctx context.Context, raw io.Reader, fn func([]byte) parsedLine) (schemas.ParseDiagnostics, error) {
	d := schemas.ParseDiagnostics{}
	reader := bufio.NewReaderSize(raw, 64<<10)
	var line []byte
	oversized := false
	for {
		if err := ctx.Err(); err != nil {
			d.Reason = "parsing cancelled"
			return d, err
		}
		fragment, err := reader.ReadSlice('\n')
		d.Bytes += int64(len(fragment))
		content := fragment
		if len(content) > 0 && content[len(content)-1] == '\n' {
			content = content[:len(content)-1]
		}
		if !oversized {
			if len(line)+len(content) > maxParserLineBytes {
				oversized = true
				line = line[:0]
			} else {
				line = append(line, content...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if oversized {
			d.Lines++
			d.Oversized++
		} else if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			d.Lines++
			result := fn(trimmed)
			if result.recognized {
				d.Recognized++
			}
			if result.malformed {
				d.Malformed++
			} else if !result.recognized {
				d.Unrecognized++
			}
			d.Emitted += result.emitted
		}
		line = line[:0]
		oversized = false
		if err != nil {
			if errors.Is(err, io.EOF) {
				return d, ctx.Err()
			}
			d.Reason = "agent stream could not be fully read"
			return d, err
		}
	}
}
