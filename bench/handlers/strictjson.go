package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// decodeJSONStrict decodes exactly one JSON value from body into v, with
// unknown fields refused. Anything after that value other than whitespace
// (a second object, malformed bytes) is an error, so a request is never
// acted on with only its first object applied.
func decodeJSONStrict(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must hold a single JSON value")
	}
	return nil
}
