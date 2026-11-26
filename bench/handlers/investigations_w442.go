package handlers

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Findings investigations (wave 4, W4-42): the reports the job
// findings.investigate writes to investigations/<job id>.json under the org
// project root. Read only; the job is started with POST /jobs.
func init() {
	RegisterOrgArea("investigations", func(env OrgEnv, r *router.Router) {
		h := &investigationsHandler{mode: env.Mode}
		if env.Cfg != nil {
			h.root = env.Cfg.Root
		}
		r.GET(APIPrefix+"/investigations", env.R(h.list))
		r.GET(APIPrefix+"/investigations/{id}", env.R(h.get))
	})
}

// InvestigationSummary is one row of GET /investigations.
type InvestigationSummary struct {
	ID        string    `json:"id"`
	ExpID     string    `json:"exp_id"`
	CreatedAt time.Time `json:"created_at"`
	Runs      int       `json:"runs"`  // runs read
	Items     int       `json:"items"` // items kept
	SpentUSD  float64   `json:"spent_usd"`
	Stopped   string    `json:"stopped,omitempty"`
}

// InvestigationList is GET /investigations.
type InvestigationList struct {
	Investigations []InvestigationSummary `json:"investigations"`
}

const investigationsListMax = 50

var investigationIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

type investigationsHandler struct {
	mode schemas.Mode
	root string
}

func (h *investigationsHandler) dir() string { return filepath.Join(h.root, "investigations") }

func (h *investigationsHandler) list(ctx *fasthttp.RequestCtx) {
	if h.root == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, "unavailable", "This server has no project folder.")
		return
	}
	exp := strings.TrimSpace(string(ctx.QueryArgs().Peek("exp")))
	out := InvestigationList{Investigations: []InvestigationSummary{}}
	entries, err := os.ReadDir(h.dir())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, "internal", "The investigations could not be read.")
		return
	}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() || !investigationIDRe.MatchString(id) {
			continue
		}
		rep, err := h.read(id)
		if err != nil || (exp != "" && rep.ExpID != exp) {
			continue
		}
		out.Investigations = append(out.Investigations, InvestigationSummary{ID: rep.ID, ExpID: rep.ExpID, CreatedAt: rep.CreatedAt,
			Runs: len(rep.Runs), Items: len(rep.Items), SpentUSD: rep.SpentUSD, Stopped: rep.Stopped})
	}
	sort.SliceStable(out.Investigations, func(i, j int) bool {
		return out.Investigations[i].CreatedAt.After(out.Investigations[j].CreatedAt)
	})
	if len(out.Investigations) > investigationsListMax {
		out.Investigations = out.Investigations[:investigationsListMax]
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *investigationsHandler) get(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	if h.root == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, "unavailable", "This server has no project folder.")
		return
	}
	if !investigationIDRe.MatchString(id) {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, "not_found", "No investigation with this name was found.")
		return
	}
	rep, err := h.read(id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, "not_found", "No investigation with this name was found.")
			return
		}
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, "internal", "The investigation could not be read.")
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, rep)
}

func (h *investigationsHandler) read(id string) (*investigator.Report, error) {
	raw, err := os.ReadFile(filepath.Join(h.dir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var rep investigator.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, err
	}
	if rep.ID == "" {
		rep.ID = id
	}
	return &rep, nil
}
