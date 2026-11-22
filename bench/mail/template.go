package mail

import (
	"fmt"
	"html"
	"strings"
)

// Row is one line of a template's table: a label and its value. Tone
// colors the value (good, bad or "" for neutral).
type Row struct {
	Label string
	Value string
	Tone  string
}

// Tones of a Row.
const (
	ToneGood = "good"
	ToneBad  = "bad"
)

// Section is a titled table in an email.
type Section struct {
	Title string
	Rows  []Row
}

// Template is a plain, branded email: a heading, a short intro, tables and
// one button. Render gives the text and HTML bodies of the same content.
type Template struct {
	Heading  string
	Intro    string
	Sections []Section
	Action   string // button label
	Link     string // button target (absolute URL)
	Footer   string // why the reader got this email and how to stop it
}

// Brand colors, from the UI's design tokens.
const (
	colorInk     = "#1b1d29"
	colorMuted   = "#5d6175"
	colorLine    = "#e6e7ee"
	colorPrimary = "#4c4fd8"
	colorGood    = "#137a4a"
	colorBad     = "#b42318"
)

// Message fills a Message with the rendered template.
func (t Template) Message(to []string, subject string) Message {
	return Message{To: to, Subject: subject, Text: t.Text(), HTML: t.HTML()}
}

// Text is the plain text body.
func (t Template) Text() string {
	var b strings.Builder
	b.WriteString(t.Heading + "\n\n")
	if t.Intro != "" {
		b.WriteString(t.Intro + "\n\n")
	}
	for _, s := range t.Sections {
		if s.Title != "" {
			b.WriteString(s.Title + "\n")
		}
		w := 0
		for _, r := range s.Rows {
			w = max(w, len(r.Label))
		}
		for _, r := range s.Rows {
			fmt.Fprintf(&b, "  %-*s  %s\n", w, r.Label, r.Value)
		}
		b.WriteString("\n")
	}
	if t.Link != "" {
		label := t.Action
		if label == "" {
			label = "Open in Heimdall"
		}
		fmt.Fprintf(&b, "%s: %s\n\n", label, t.Link)
	}
	if t.Footer != "" {
		b.WriteString("-- \n" + t.Footer + "\n")
	}
	return b.String()
}

// HTML is the HTML body: one centred card, inline styles only (mail
// clients drop style sheets), every value escaped.
func (t Template) HTML() string {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">`)
	b.WriteString(`<title>` + e(t.Heading) + `</title></head>`)
	fmt.Fprintf(&b, `<body style="margin:0;padding:0;background:#f6f7fb;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Inter,Helvetica,Arial,sans-serif;color:%s">`, colorInk)
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f6f7fb;padding:24px 12px"><tr><td align="center">`)
	fmt.Fprintf(&b, `<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="max-width:560px;background:#ffffff;border:1px solid %s;border-radius:10px">`, colorLine)
	fmt.Fprintf(&b, `<tr><td style="padding:20px 28px 0 28px;font-size:13px;font-weight:600;letter-spacing:.02em;color:%s">Heimdall</td></tr>`, colorPrimary)
	fmt.Fprintf(&b, `<tr><td style="padding:8px 28px 0 28px;font-size:20px;line-height:28px;font-weight:600">%s</td></tr>`, e(t.Heading))
	if t.Intro != "" {
		fmt.Fprintf(&b, `<tr><td style="padding:8px 28px 0 28px;font-size:14px;line-height:22px;color:%s">%s</td></tr>`, colorMuted, e(t.Intro))
	}
	for _, s := range t.Sections {
		b.WriteString(`<tr><td style="padding:20px 28px 0 28px">`)
		if s.Title != "" {
			fmt.Fprintf(&b, `<div style="font-size:12px;font-weight:600;text-transform:uppercase;letter-spacing:.04em;color:%s;padding-bottom:6px">%s</div>`, colorMuted, e(s.Title))
		}
		b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="font-size:14px;line-height:20px">`)
		for _, r := range s.Rows {
			c := colorInk
			switch r.Tone {
			case ToneGood:
				c = colorGood
			case ToneBad:
				c = colorBad
			}
			fmt.Fprintf(&b, `<tr><td style="padding:6px 0;border-top:1px solid %s;color:%s">%s</td><td align="right" style="padding:6px 0;border-top:1px solid %s;color:%s;font-weight:500;font-variant-numeric:tabular-nums">%s</td></tr>`,
				colorLine, colorMuted, e(r.Label), colorLine, c, e(r.Value))
		}
		b.WriteString(`</table></td></tr>`)
	}
	if t.Link != "" {
		label := t.Action
		if label == "" {
			label = "Open in Heimdall"
		}
		fmt.Fprintf(&b, `<tr><td style="padding:24px 28px 0 28px"><a href="%s" style="display:inline-block;background:%s;color:#ffffff;text-decoration:none;font-size:14px;font-weight:600;padding:10px 16px;border-radius:6px">%s</a></td></tr>`,
			e(t.Link), colorPrimary, e(label))
	}
	b.WriteString(`<tr><td style="padding:24px 28px 0 28px"></td></tr>`)
	if t.Footer != "" {
		fmt.Fprintf(&b, `<tr><td style="padding:0 28px 20px 28px;font-size:12px;line-height:18px;color:%s;border-top:1px solid %s;padding-top:14px">%s</td></tr>`, colorMuted, colorLine, e(t.Footer))
	}
	b.WriteString(`</table></td></tr></table></body></html>`)
	return b.String()
}
