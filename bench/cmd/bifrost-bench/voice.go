package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/voice"
)

// X8 voice agents (docs/voice-agents.md).

const voiceUsage = `usage: bifrost-bench voice check [-config deploy/bench.yaml] -suite <suite id> [-json]
       bifrost-bench voice check -timeline <timeline.jsonl> [-barge-in-ms 1000] [-dead-air-ms 2000] [-json]
check -suite lints every voice task of a suite (the voice: block, the caller service, its exports, the probes it allows) and inspects each caller image with no network: the voice caller's files, the Piper voice and the Whisper model it needs. It calls no model.
check -timeline reads one call timeline (state/<caller>/timeline.jsonl of a run) and prints what the voice metrics read from it: time to first audio, every turn with its latency, barge-ins, dead air.
Exit 0 when everything is fine, 1 when a check fails.`

func init() {
	register("voice", "voice agents: check a voice suite and its caller images, or read a call timeline (voice check)", runVoice)
}

func runVoice(args []string) int {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprintln(os.Stderr, voiceUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet("voice check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	config := fs.String("config", defaultConfig, "bench config")
	suiteID := fs.String("suite", "", "suite whose voice tasks to check")
	timeline := fs.String("timeline", "", "a call timeline to read")
	bargeIn := fs.Int("barge-in-ms", voice.DefaultBargeInBoundMS, "barge-in bound for -timeline")
	deadAir := fs.Int("dead-air-ms", voice.DefaultDeadAirMS, "dead air bound for -timeline")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || (*suiteID == "") == (*timeline == "") {
		fmt.Fprintln(os.Stderr, voiceUsage)
		return exitUsage
	}
	if *timeline != "" {
		return voiceTimeline(*timeline, &schemas.VoiceSpec{BargeInBoundMS: *bargeIn, DeadAirMS: *deadAir}, *asJSON)
	}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	s, err := engine.LoadSuite(ctx, a.reg, *suiteID)
	if err != nil {
		return fail("voice check: load suite %s: %v", *suiteID, err)
	}
	rows := voiceSuiteRows(ctx, s, dockerImageCheck)
	bad := 0
	for _, r := range rows {
		if !r.OK {
			bad++
		}
	}
	if *asJSON {
		if code := printJSON(map[string]any{"suite": s.ID, "rows": rows, "ok": bad == 0}, "voice check"); code != exitOK {
			return code
		}
	} else {
		w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "CHECK\tTASK\tRESULT\tDETAIL")
		for _, r := range rows {
			res := "ok"
			if !r.OK {
				res = "FAIL"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Check, r.Task, res, r.Detail)
		}
		_ = w.Flush()
	}
	if bad > 0 {
		return 1
	}
	return exitOK
}

// voiceRow is one line of voice check -suite.
type voiceRow struct {
	Check  string `json:"check"`
	Task   string `json:"task,omitempty"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// imageCheck inspects a caller image; it returns a detail and whether the
// image is fit.
type imageCheck func(ctx context.Context, image string) (string, bool)

func voiceSuiteRows(ctx context.Context, s *schemas.Suite, inspect imageCheck) []voiceRow {
	var rows []voiceRow
	images := map[string]bool{}
	n := 0
	for _, t := range s.Tasks {
		if !voice.IsVoiceTask(t) {
			continue
		}
		n++
		spec := voice.Effective(t.Voice)
		if err := voice.CheckTask(s, t); err != nil {
			rows = append(rows, voiceRow{Check: "task", Task: t.ID, Detail: err.Error()})
			continue
		}
		probes := "every voice probe"
		if len(spec.Probes) > 0 {
			probes = strings.Join(spec.Probes, ", ")
		}
		rows = append(rows, voiceRow{Check: "task", Task: t.ID, OK: true,
			Detail: fmt.Sprintf("caller %s, %s, barge-in bound %d ms, dead air %d ms, probes: %s", spec.Caller, voice.Protocol, spec.BargeInBoundMS, spec.DeadAirMS, probes)})
		if c, ok := voice.CallerService(s, t); ok && c.Image != "" {
			images[c.Image] = true
		}
	}
	if n == 0 {
		return []voiceRow{{Check: "suite", Detail: "no task has a voice: block"}}
	}
	list := make([]string, 0, len(images))
	for im := range images {
		list = append(list, im)
	}
	sort.Strings(list)
	for _, im := range list {
		detail, ok := inspect(ctx, im)
		rows = append(rows, voiceRow{Check: "caller image " + im, OK: ok, Detail: detail})
	}
	return rows
}

// callerProbe runs inside a caller image with no network and reports what
// the caller needs: its files, Piper and the Whisper model.
const callerProbe = `import json, os, sys
sys.path.insert(0, "/srv")
out = {}
for f in ("voicecaller.py", "tonecode.py", "wsproto.py", "voiceclient.py", "simuser.py"):
    out[f] = os.path.exists("/srv/" + f)
out["piper"] = os.path.exists(os.environ.get("PIPER_BIN", "/opt/voice/piper/piper")) and os.path.exists(os.environ.get("PIPER_VOICE", "/opt/voice/voices/en_US-ljspeech-medium.onnx"))
out["whisper"] = os.path.exists(os.path.join(os.environ.get("WHISPER_MODEL_DIR", "/opt/voice/whisper"), "model.bin"))
try:
    import faster_whisper  # noqa: F401
    out["faster_whisper"] = True
except ImportError:
    out["faster_whisper"] = False
import voicecaller
out["protocol"] = voicecaller.PROTOCOL
print(json.dumps(out))
`

func dockerImageCheck(ctx context.Context, image string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--entrypoint", "python3", image, "-I", "-c", callerProbe).Output()
	if err != nil {
		return fmt.Sprintf("cannot run the image (build it first; suite admit does): %v", err), false
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		return "the image's probe printed no JSON", false
	}
	return callerImageVerdict(got)
}

// callerImageVerdict reads the in-image probe's answer.
func callerImageVerdict(got map[string]any) (string, bool) {
	var missing []string
	for _, f := range []string{"voicecaller.py", "tonecode.py", "wsproto.py", "voiceclient.py", "simuser.py"} {
		if got[f] != true {
			missing = append(missing, "/srv/"+f)
		}
	}
	if len(missing) > 0 {
		return "missing " + strings.Join(missing, ", "), false
	}
	if got["protocol"] != voice.Protocol {
		return fmt.Sprintf("the caller speaks %v, this bench %s", got["protocol"], voice.Protocol), false
	}
	var notes []string
	if got["piper"] != true {
		notes = append(notes, "no Piper voice: only BENCH_VOICE_TTS=tone works")
	}
	if got["whisper"] != true || got["faster_whisper"] != true {
		notes = append(notes, "no Whisper: the caller reads tone-coded agent audio only")
	}
	if len(notes) == 0 {
		return voice.Protocol + ", Piper and Whisper present", true
	}
	return voice.Protocol + "; " + strings.Join(notes, "; "), true
}

func voiceTimeline(path string, spec *schemas.VoiceSpec, asJSON bool) int {
	ev, err := voice.ReadTimeline(path)
	if err != nil {
		return fail("voice check: %v", err)
	}
	a, err := voice.Analyze(ev, spec)
	if err != nil {
		return fail("voice check: %v", err)
	}
	if asJSON {
		return printJSON(a, "voice check")
	}
	printVoiceAnalysis(os.Stdout, a)
	if !a.Complete {
		return 1
	}
	return exitOK
}

func printVoiceAnalysis(out io.Writer, a *voice.Analysis) {
	ms := func(v *float64) string {
		if v == nil {
			return "none"
		}
		return fmt.Sprintf("%.0f ms", *v)
	}
	fmt.Fprintf(out, "call: %.1f s, ended by %s, complete %v\n", a.CallMS/1000, a.EndedBy, a.Complete)
	fmt.Fprintf(out, "time to first audio: %s\n", ms(a.FirstAudioMS))
	l := a.Latencies()
	if len(l) > 0 {
		fmt.Fprintf(out, "turn latency: p50 %.0f ms, p95 %.0f ms over %d answered turns\n", voice.Percentile(l, 50), voice.Percentile(l, 95), len(l))
	} else {
		fmt.Fprintln(out, "turn latency: no answered turn")
	}
	fmt.Fprintf(out, "dead air: %.1f s (gaps over %d ms)\n", a.DeadAirMS/1000, a.DeadAirBoundMS)
	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TURN\tFLOOR AT\tAGENT STARTED\tGAP\tDEAD AIR")
	for _, t := range a.Turns {
		fmt.Fprintf(w, "%s %d\t%.0f ms\t%s\t%.0f ms\t%v\n", t.Kind, t.CallerTurn, t.FloorMS, ms(t.AgentStartMS), t.GapMS, t.DeadAir)
	}
	_ = w.Flush()
	for _, b := range a.BargeIns {
		fmt.Fprintf(out, "barge-in on utterance %d: agent stopped %.0f ms after the caller started (%s), handled %v (bound %d ms)\n",
			b.Utt, b.StoppedAfterMS, b.By, b.Handled, a.BargeInBoundMS)
	}
	for _, line := range a.Lines {
		fmt.Fprintf(out, "caller %d (%s, %s): %s\n", line.Turn, line.Kind, line.TTS, line.Text)
	}
	for _, u := range a.Utterances {
		text := "(not transcribed)"
		if u.Text != nil {
			text = *u.Text
		}
		fmt.Fprintf(out, "agent %d (%s%s): %s\n", u.Utt, u.STT, map[bool]string{true: ", interrupted"}[u.Interrupted], text)
	}
}
