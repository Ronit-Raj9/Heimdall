package adapters

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestScanLinesLimitAndRecovery(t *testing.T) {
	input := strings.Repeat("x", maxParserLineBytes) + "\n" + strings.Repeat("x", maxParserLineBytes+1) + "\n" + "héllo"
	var lines []string
	d, err := scanLines(context.Background(), []byte(input), func(line []byte) parsedLine {
		if !utf8.Valid(line) {
			t.Fatal("reader split a rune")
		}
		lines = append(lines, string(line))
		return parsedLine{recognized: true, emitted: 1}
	})
	if err != nil || len(lines) != 2 || lines[1] != "héllo" || d.Bytes != int64(len(input)) || d.Lines != 3 || d.Oversized != 1 {
		t.Fatalf("lines=%d diagnostics=%+v err=%v", len(lines), d, err)
	}
}

func TestScanLinesCancellationKeepsCounts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d, err := scanLines(ctx, []byte("one\ntwo\n"), func(line []byte) parsedLine {
		cancel()
		return parsedLine{recognized: true, emitted: 1}
	})
	if !errors.Is(err, context.Canceled) || d.Lines != 1 || d.Bytes != 4 || d.Emitted != 1 {
		t.Fatalf("diagnostics=%+v err=%v", d, err)
	}
}

func TestParsersReportDamageAndContinue(t *testing.T) {
	for _, p := range BuiltinParsers() {
		t.Run(p.GetName(), func(t *testing.T) {
			res, err := p.Parse(context.Background(), nil, []byte("{bad json\n"+strings.Repeat("x", maxParserLineBytes+1)+"\n{}\n"))
			if err != nil || res.Diagnostics == nil || res.Diagnostics.Malformed != 1 || res.Diagnostics.Oversized != 1 || res.Diagnostics.Unrecognized != 1 {
				t.Fatalf("result=%+v err=%v", res, err)
			}
		})
	}
}
