package main

// X1 vision grading (docs/visual-grading.md): image content blocks, so the
// vision judge can be scripted at $0. A request's images are the Anthropic
// Messages blocks {"type": "image", "source": {"type": "base64", "data": ...}}
// and the OpenAI Chat Completions parts {"type": "image_url", "image_url":
// {"url": "data:image/png;base64,..."}}; each is named by the sha256 of its
// decoded bytes. The script's by_image_sha256 maps the first image's hash
// (the vision judge sends the capture first) to a reply, ahead of every
// other rule.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// imageHashes returns the sha256 of every image block of a message content,
// in order.
func imageHashes(raw json.RawMessage) []string {
	var parts []struct {
		Type   string `json:"type"`
		Source *struct {
			Type string `json:"type"`
			Data string `json:"data"`
		} `json:"source"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return nil
	}
	var out []string
	for _, p := range parts {
		data := ""
		switch {
		case p.Type == "image" && p.Source != nil && p.Source.Type == "base64":
			data = p.Source.Data
		case p.Type == "image_url" && p.ImageURL != nil:
			if _, b64, ok := strings.Cut(p.ImageURL.URL, ";base64,"); ok {
				data = b64
			}
		}
		if data == "" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			continue
		}
		h := sha256.Sum256(b)
		out = append(out, hex.EncodeToString(h[:]))
	}
	return out
}

// byImage is the script's reply for the first image, if it names one.
func byImage(sc *script, images []string) (string, bool) {
	if len(images) == 0 || sc.ByImageSHA256 == nil {
		return "", false
	}
	r, ok := sc.ByImageSHA256[images[0]]
	return r, ok
}
