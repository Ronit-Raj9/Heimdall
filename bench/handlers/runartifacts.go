package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
)

// ArtifactReader is optionally implemented by a ReadIndex. RunArtifact
// returns the bytes of artifacts/<id>/<name> in a run folder, or an error
// wrapping schemas.ErrNotFound when the run, the file or a safe path to it
// does not exist. Implementations must refuse an id or name that fails the
// artifact naming rules (utils.CheckArtifactSpec, utils.ArtifactNameOK) and
// never follow a symlink. Without it the artifact route answers 503.
type ArtifactReader interface {
	RunArtifact(runID, id, name string) ([]byte, error)
}

// getRunArtifact handles GET /api/bench/runs/{id}/artifacts/{artifact}/{name}:
// one artifact file the agent produced, as an attachment. It is served only
// from a sealed run, only when evidence.json lists the file as copied without
// error at artifacts/<artifact>/<name>, and only when its bytes hash to both
// the evidence entry and the run's seal.json. The body is agent-written
// content: it is always application/octet-stream, sniffing is off and the
// browser is told to download it, never to render it.
func (h *RunsHandler) getRunArtifact(ctx *fasthttp.RequestCtx) {
	id, name := pathParam(ctx, "artifact"), pathParam(ctx, "name")
	if !utils.ArtifactIDOK(id) || !utils.ArtifactNameOK(name) {
		sendNotFound(ctx, h.mode, "artifact", id+"/"+name)
		return
	}
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	runID := rv.Run.RunID
	if !rv.Sealed {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
			"run "+runID+" is not sealed: artifact files are served only from a sealed run")
		return
	}
	reader, isReader := h.index.(ArtifactReader)
	if !isReader {
		sendUnavailable(ctx, h.mode, "artifact file reader")
		return
	}
	entry, found, err := evidenceArtifact(h.index, runID, id, name)
	if err != nil {
		sendDepError(ctx, h.mode, "read evidence of run "+runID, err)
		return
	}
	if !found {
		sendNotFound(ctx, h.mode, "artifact of run "+runID, id+"/"+name)
		return
	}
	if entry.Error != "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound,
			"artifact "+id+"/"+name+" of run "+runID+" was not exported: "+entry.Error)
		return
	}
	sealed, err := sealedFileHash(h.index, runID, entry.Path)
	if err != nil {
		sendArtifactConflict(ctx, h.mode, runID, id, name, err)
		return
	}
	if sv, isVerifier := h.index.(SealVerifier); isVerifier {
		wctx, cancel := workContext(ctx, readTimeout)
		err := sv.VerifySeal(wctx, runID)
		cancel()
		if err != nil {
			if errors.Is(err, schemas.ErrSealMismatch) {
				sendArtifactConflict(ctx, h.mode, runID, id, name, err)
				return
			}
			sendDepError(ctx, h.mode, "verify seal of run "+runID, err)
			return
		}
	}
	data, err := reader.RunArtifact(runID, id, name)
	if err != nil {
		if errors.Is(err, schemas.ErrNotFound) {
			sendNotFound(ctx, h.mode, "artifact file of run "+runID, id+"/"+name)
			return
		}
		sendDepError(ctx, h.mode, "read artifact of run "+runID, err)
		return
	}
	got := utils.SHA256Bytes(data)
	if got != bareHash(entry.SHA256) || got != bareHash(sealed) {
		sendArtifactConflict(ctx, h.mode, runID, id, name,
			fmt.Errorf("file hashes to %s, evidence says %s, seal says %s: %w", got, entry.SHA256, sealed, schemas.ErrSealMismatch))
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/octet-stream")
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.Response.Header.Set("Content-Security-Policy", "sandbox")
	// The name passed utils.ArtifactNameOK: letters, digits, '.', '_' and
	// '-' only, so it needs no quoting or escaping.
	ctx.Response.Header.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	ctx.SetBody(data)
}

// evidenceArtifact finds the evidence.json entry of one artifact file. An
// entry counts only when its path is exactly utils.ArtifactRunPath(id,
// name), so a recorded path can never point elsewhere in the run folder. A
// run without evidence, or without the entry, returns found false.
func evidenceArtifact(index ReadIndex, runID, id, name string) (schemas.ArtifactFile, bool, error) {
	var ev struct {
		Artifacts []schemas.ArtifactFile `json:"artifacts"`
	}
	data, found, err := readInternal(index, runID, FileEvidence)
	if err != nil || !found {
		return schemas.ArtifactFile{}, false, err
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return schemas.ArtifactFile{}, false, fmt.Errorf("parse %s of run %s: %w", FileEvidence, runID, err)
	}
	want := utils.ArtifactRunPath(id, name)
	for _, a := range ev.Artifacts {
		if a.ID != id || a.Name != name {
			continue
		}
		if a.Error != "" {
			return a, true, nil
		}
		if a.Path == want {
			return a, true, nil
		}
	}
	return schemas.ArtifactFile{}, false, nil
}

// sealedFileHash returns the sha256 seal.json records for one run folder
// path. A sealed run without seal.json, or a path the seal does not list, is
// an error wrapping schemas.ErrSealMismatch: the file is not sealed evidence.
func sealedFileHash(index ReadIndex, runID, path string) (string, error) {
	var seal schemas.SealRecord
	found, err := readJSONFile(index, runID, FileSeal, &seal)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("run has no %s: %w", FileSeal, schemas.ErrSealMismatch)
	}
	sum, ok := seal.Files[path]
	if !ok || sum == "" {
		return "", fmt.Errorf("%s is not in %s: %w", path, FileSeal, schemas.ErrSealMismatch)
	}
	return sum, nil
}

// sendArtifactConflict answers an artifact whose sealed evidence does not
// hold: 409 when the file, its evidence entry and the seal disagree, any
// other failure as a dependency error. The file is never served.
func sendArtifactConflict(ctx *fasthttp.RequestCtx, mode schemas.Mode, runID, id, name string, err error) {
	what := "artifact " + id + "/" + name + " of run " + runID
	if errors.Is(err, schemas.ErrSealMismatch) {
		lib.SendError(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, what+" does not match its seal: "+err.Error())
		return
	}
	sendDepError(ctx, mode, "check seal of "+what, err)
}

// bareHash drops a "sha256:" prefix, so a hash compares the same with or
// without it.
func bareHash(sum string) string {
	return strings.TrimPrefix(sum, "sha256:")
}
