package lib

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func TestWithMode(t *testing.T) {
	tests := []struct {
		name, in, want string
		err            bool
	}{
		{"object", `{"a":1}`, `{"mode":"live","a":1}`, false},
		{"empty object", `{}`, `{"mode":"live"}`, false},
		{"already has mode", `{"mode":"recorded","a":1}`, `{"mode":"recorded","a":1}`, false},
		{"array", `[1]`, "", true},
		{"null", `null`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := WithMode([]byte(tt.in), schemas.ModeLive)
			if (err != nil) != tt.err {
				t.Fatalf("err = %v", err)
			}
			if !tt.err && string(got) != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSendJSONAndError(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	SendJSON(ctx, schemas.ModeSynthetic, fasthttp.StatusCreated, struct {
		ID string `json:"id"`
	}{"x"})
	var body map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
		t.Fatal(err)
	}
	if ctx.Response.StatusCode() != 201 || body["mode"] != "synthetic" || body["id"] != "x" ||
		string(ctx.Response.Header.Peek(ModeHeader)) != "synthetic" {
		t.Fatalf("response %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}

	ctx = &fasthttp.RequestCtx{}
	SendJSON(ctx, schemas.ModeLive, 200, []int{1}) // not an object: a programming error
	if ctx.Response.StatusCode() != 500 {
		t.Errorf("non-object payload status = %d", ctx.Response.StatusCode())
	}

	ctx = &fasthttp.RequestCtx{}
	SendError(ctx, schemas.ModeLive, fasthttp.StatusNotFound, ErrTypeNotFound, "run not found: r1")
	var eb ErrorBody
	if err := json.Unmarshal(ctx.Response.Body(), &eb); err != nil {
		t.Fatal(err)
	}
	if eb.Mode != schemas.ModeLive || eb.Error.Type != ErrTypeNotFound || eb.Error.Message != "run not found: r1" {
		t.Fatalf("error body = %+v", eb)
	}
}
