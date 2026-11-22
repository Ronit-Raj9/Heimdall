package main

// import registry / import fetch (lane Z09): resolve Harbor registry names
// (name@version) and Hugging Face datasets to local folders the importers
// read. These are the only import commands that use the network; the
// importers themselves read local data.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/importers/harbor"
	"github.com/maximhq/bifrost/bench/importers/hfsource"
)

const importFetchUsage = `usage: bifrost-bench import registry [-registry <file|url>] [-kind agent|model_test|all] [-json]
       bifrost-bench import fetch harbor <name@version> -to <dir> [-registry <file|url>] [-ids a,b] [-limit n]
       bifrost-bench import fetch hf <org/name|url> -to <dir> [-config c] [-split s] [-max n] [-suite <id>]`

// runImportRegistry lists the Harbor registry, agent datasets first.
func runImportRegistry(args []string) int {
	fs := flag.NewFlagSet("import registry", flag.ContinueOnError)
	src := fs.String("registry", "", "registry.json file or URL (default Harbor's published registry)")
	kind := fs.String("kind", harbor.KindAgent, "agent, model_test, example or all")
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parseArgs(fs, args); err != nil {
		fmt.Fprintln(os.Stderr, importFetchUsage)
		return exitUsage
	}
	reg, err := harbor.LoadRegistry(context.Background(), *src, nil)
	if err != nil {
		return fail("%v", err)
	}
	type row struct {
		Ref         string `json:"ref"`
		Kind        string `json:"kind"`
		Tasks       int    `json:"tasks"`
		License     string `json:"license"`
		Description string `json:"description,omitempty"`
	}
	var rows []row
	for _, d := range reg.Sorted() {
		if *kind != "all" && d.Kind() != *kind {
			continue
		}
		rows = append(rows, row{d.Ref(), d.Kind(), len(d.Tasks), d.License(), d.Description})
	}
	if *asJSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Println(string(b))
		return exitOK
	}
	for _, r := range rows {
		desc := r.Description
		if len(desc) > 80 {
			desc = desc[:77] + "..."
		}
		fmt.Printf("%-34s %-10s %5d  %-11s %s\n", r.Ref, r.Kind, r.Tasks, r.License, desc)
	}
	return exitOK
}

// runImportFetch downloads a Harbor dataset or a Hugging Face split.
func runImportFetch(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, importFetchUsage)
		return exitUsage
	}
	switch args[0] {
	case "harbor":
		return runFetchHarbor(args[1:])
	case "hf":
		return runFetchHF(args[1:])
	}
	fmt.Fprintln(os.Stderr, importFetchUsage)
	return exitUsage
}

func runFetchHarbor(args []string) int {
	fs := flag.NewFlagSet("import fetch harbor", flag.ContinueOnError)
	to := fs.String("to", "", "folder to write (tasks/ and harbor-dataset.json)")
	src := fs.String("registry", "", "registry.json file or URL (default Harbor's published registry)")
	ids := fs.String("ids", "", "comma-separated task names (default: all)")
	limit := fs.Int("limit", 0, "fetch at most this many tasks")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 || *to == "" {
		fmt.Fprintln(os.Stderr, importFetchUsage)
		return exitUsage
	}
	var idList []string
	if *ids != "" {
		idList = strings.Split(*ids, ",")
	}
	m, err := fetchHarborRef(pos[0], *to, *src, idList, *limit)
	if err != nil {
		return fail("%v", err)
	}
	fmt.Printf("fetched %s: %d task(s) into %s (licence %s, %s)\n", m.Benchmark, m.Tasks, *to, m.License, m.Kind)
	fmt.Printf("  next: bifrost-bench import harbor -from %s -suite <id>\n", *to)
	return exitOK
}

// fetchHarborRef resolves ref in the registry and fetches it into dir.
func fetchHarborRef(ref, dir, src string, ids []string, limit int) (*harbor.Manifest, error) {
	ctx := context.Background()
	reg, err := harbor.LoadRegistry(ctx, src, nil)
	if err != nil {
		return nil, err
	}
	d, err := reg.Resolve(ref)
	if err != nil {
		return nil, err
	}
	if src == "" {
		src = harbor.RegistryURL
	}
	return harbor.Fetch(ctx, d, dir, harbor.FetchOptions{IDs: ids, Limit: limit, Registry: src, HFToken: os.Getenv("HF_TOKEN")})
}

func runFetchHF(args []string) int {
	fs := flag.NewFlagSet("import fetch hf", flag.ContinueOnError)
	to := fs.String("to", "", "folder to write (<config>-<split>.jsonl and SOURCE.md)")
	config := fs.String("config", "", "dataset config (default: the first)")
	split := fs.String("split", "", "split (default: test, else validation, else the first)")
	max := fs.Int("max", 0, "fetch at most this many rows (default all)")
	suiteID := fs.String("suite", "", "also import into this suite when the route is swebench")
	cfgFlag := fs.String("config-file", defaultConfig, "bench config (with -suite)")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 || *to == "" {
		fmt.Fprintln(os.Stderr, importFetchUsage)
		return exitUsage
	}
	ds, err := hfsource.ParseDataset(pos[0])
	if err != nil {
		return fail("%v", err)
	}
	ctx := context.Background()
	c := &hfsource.Client{Token: os.Getenv("HF_TOKEN")}
	splits, err := c.Splits(ctx, ds)
	if err != nil {
		return fail("%v", err)
	}
	s, err := pickSplit(splits, *config, *split)
	if err != nil {
		return fail("%s: %v", ds, err)
	}
	info, err := c.Info(ctx, s)
	if err != nil {
		info = nil // /info is missing for some sets; the rows still come
	}
	if err := os.MkdirAll(*to, 0o755); err != nil {
		return fail("%v", err)
	}
	out := filepath.Join(*to, s.Config+"-"+s.Split+".jsonl")
	f, err := os.Create(out)
	if err != nil {
		return fail("%v", err)
	}
	res, err := c.Fetch(ctx, s, *max, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fail("%v", err)
	}
	if err := hfsource.WriteSource(*to, s, info, res, time.Now()); err != nil {
		return fail("%v", err)
	}
	lic := "not stated"
	if info != nil && info.License != "" {
		lic = info.License
	}
	fmt.Printf("fetched %s %s/%s: %d of %d rows into %s (licence %s)\n", ds, s.Config, s.Split, res.Rows, res.Total, out, lic)
	fmt.Printf("  route: %s (%s)\n", orNoneStr(res.Route.Importer), res.Route.Reason)
	switch res.Route.Importer {
	case "rows":
		fmt.Printf("  next: bifrost-bench task import-rows -suite <qa suite> -file %s\n", out)
	case "":
	default:
		if *suiteID == "" || res.Route.Importer != "swebench" {
			fmt.Printf("  next: bifrost-bench import %s -from %s -suite <id>\n", res.Route.Importer, out)
			return exitOK
		}
		return runImport([]string{"swebench", "-config", *cfgFlag, "-from", out, "-suite", *suiteID, "-set", "source=https://huggingface.co/datasets/" + ds})
	}
	return exitOK
}

// pickSplit chooses config and split; empty means the first config and the
// test split, else validation, else the first.
func pickSplit(splits []hfsource.Split, config, split string) (hfsource.Split, error) {
	if len(splits) == 0 {
		return hfsource.Split{}, fmt.Errorf("no splits (the dataset viewer may not support this dataset)")
	}
	if config == "" {
		config = splits[0].Config
	}
	var inConfig []hfsource.Split
	for _, s := range splits {
		if s.Config == config {
			inConfig = append(inConfig, s)
		}
	}
	if len(inConfig) == 0 {
		return hfsource.Split{}, fmt.Errorf("no config %q", config)
	}
	want := []string{split}
	if split == "" {
		want = []string{"test", "validation", inConfig[0].Split}
	}
	for _, w := range want {
		for _, s := range inConfig {
			if s.Split == w {
				return s, nil
			}
		}
	}
	var names []string
	for _, s := range inConfig {
		names = append(names, s.Split)
	}
	return hfsource.Split{}, fmt.Errorf("config %s has no split %q (splits: %s)", config, split, strings.Join(names, ", "))
}

func orNoneStr(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// harborFromRef turns `import harbor -from name@version` into a local
// folder: when from is no existing path but a registry name, it is fetched
// into <root>/datasets/harbor/<name@version> (kept, so a later import
// reuses it).
func harborFromRef(from, root string) (string, error) {
	if _, err := os.Stat(from); err == nil || !harbor.LooksLikeRef(from) {
		return from, nil
	}
	dir := filepath.Join(root, "datasets", "harbor", from)
	if _, err := os.Stat(filepath.Join(dir, harbor.ManifestFile)); err == nil {
		return dir, nil
	}
	m, err := fetchHarborRef(from, dir, "", nil, 0)
	if err != nil {
		return "", err
	}
	fmt.Printf("fetched %s: %d task(s) into %s\n", m.Benchmark, m.Tasks, dir)
	return dir, nil
}
