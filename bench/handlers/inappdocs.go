package handlers

import (
	"bufio"
	"bytes"
	"strings"

	"github.com/fasthttp/router"
	benchdocs "github.com/maximhq/bifrost/bench/docs"
	"github.com/maximhq/bifrost/bench/examples"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/spec"
	"github.com/valyala/fasthttp"
)

// DocSummary is one in-app guide in GET /docs.
type DocSummary struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	// Snippet is, for a search (q), the first line of the guide that
	// contains q, without markdown marks, cut to about 160 characters.
	Snippet string `json:"snippet,omitempty"`
}

// DocList is GET /docs: every guide embedded in the server binary.
type DocList struct {
	Docs []DocSummary `json:"docs"`
}

// DocBody is GET /docs/{name}: one guide's markdown.
type DocBody struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
}

func init() {
	RegisterOrgArea("inappdocs", func(env OrgEnv, r *router.Router) {
		mode := env.Mode
		r.GET(APIPrefix+"/docs", env.R(func(ctx *fasthttp.RequestCtx) { listDocs(ctx, mode) }))
		r.GET(APIPrefix+"/docs/{name}", env.R(func(ctx *fasthttp.RequestCtx) { getDoc(ctx, mode) }))
	})
}

func listDocs(ctx *fasthttp.RequestCtx, mode schemas.Mode) {
	q, ok, msg := docSearchQuery(string(ctx.QueryArgs().Peek("q")))
	if !ok {
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
		return
	}
	lib.SendJSON(ctx, mode, fasthttp.StatusOK, DocList{Docs: searchDocs(benchdocs.Names(), benchdocs.Read, q)})
}

func getDoc(ctx *fasthttp.RequestCtx, mode schemas.Mode) {
	name, _ := ctx.UserValue("name").(string)
	name = strings.TrimSuffix(name, ".md")
	if !validDocName(name) {
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"doc name must be letters, digits, '-', '_' or '.', for example quickstart")
		return
	}
	data, ok := readGuide(name, false)
	if !ok {
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound,
			"no guide named "+name+": GET /api/bench/docs lists them")
		return
	}
	lib.SendJSON(ctx, mode, fasthttp.StatusOK, DocBody{Name: name, Title: docTitle(name, data), Markdown: string(data)})
}

// readGuide returns one guide's markdown: an embedded docs/*.md guide (only
// the public ones when public is set), or one of the extra guides built from
// files the guides link to outside the docs folder (extraGuide).
func readGuide(name string, public bool) ([]byte, bool) {
	if public {
		if data, ok := benchdocs.ReadPublic(name); ok {
			return data, true
		}
	} else if data, ok := benchdocs.Read(name); ok {
		return data, true
	}
	return extraGuide(name)
}

// extraGuide serves, by name, the files the guides link to with a path
// outside the docs folder ("../spec/agent-event.schema.json",
// "../examples/acme-node/README.md"), which the server has no route for. They
// are readable in the app and on the public site but not listed.
func extraGuide(name string) ([]byte, bool) {
	switch name {
	case "agent-event-schema":
		schema, err := spec.FS.ReadFile("agent-event.schema.json")
		if err != nil {
			return nil, false
		}
		var b strings.Builder
		b.WriteString("# Agent event schema\n\n")
		b.WriteString("The JSON Schema of one agent event, the object an agent reports about one of its own steps (see [Agent events](agent-events.md)).\n\n")
		b.WriteString("```json\n")
		b.Write(bytes.TrimRight(schema, "\n"))
		b.WriteString("\n```\n")
		return []byte(b.String()), true
	case "example-acme-node":
		if len(examples.AcmeNodeREADME) == 0 {
			return nil, false
		}
		return examples.AcmeNodeREADME, true
	}
	return nil, false
}

// validDocName accepts plain file stems only: no separators, no "..", at most 100 bytes.
func validDocName(name string) bool {
	if name == "" || len(name) > 100 || strings.Contains(name, "..") {
		return false
	}
	for _, c := range name {
		ok := c == '-' || c == '_' || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// docTitle is the first "# " heading, or the name when the guide has none.
func docTitle(name string, data []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), benchdocs.MaxBytes)
	for i := 0; sc.Scan() && i < 40; i++ {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return name
}
