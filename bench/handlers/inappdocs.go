package handlers

import (
	"bufio"
	"bytes"
	"strings"

	"github.com/fasthttp/router"
	benchdocs "github.com/maximhq/bifrost/bench/docs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// DocSummary is one in-app guide in GET /docs.
type DocSummary struct {
	Name  string `json:"name"`
	Title string `json:"title"`
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
	names := benchdocs.Names()
	out := DocList{Docs: make([]DocSummary, 0, len(names))}
	for _, n := range names {
		data, ok := benchdocs.Read(n)
		if !ok {
			continue
		}
		out.Docs = append(out.Docs, DocSummary{Name: n, Title: docTitle(n, data)})
	}
	lib.SendJSON(ctx, mode, fasthttp.StatusOK, out)
}

func getDoc(ctx *fasthttp.RequestCtx, mode schemas.Mode) {
	name, _ := ctx.UserValue("name").(string)
	name = strings.TrimSuffix(name, ".md")
	if !validDocName(name) {
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"doc name must be letters, digits, '-', '_' or '.', for example quickstart")
		return
	}
	data, ok := benchdocs.Read(name)
	if !ok {
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound,
			"no guide named "+name+": GET /api/bench/docs lists them")
		return
	}
	lib.SendJSON(ctx, mode, fasthttp.StatusOK, DocBody{Name: name, Title: docTitle(name, data), Markdown: string(data)})
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
