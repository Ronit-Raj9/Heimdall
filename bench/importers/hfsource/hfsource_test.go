package hfsource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestParseDataset(t *testing.T) {
	for in, want := range map[string]string{
		"princeton-nlp/SWE-bench_Verified":                          "princeton-nlp/SWE-bench_Verified",
		"https://huggingface.co/datasets/xlangai/longmemeval":       "xlangai/longmemeval",
		"https://huggingface.co/datasets/org/name/tree/main/subdir": "org/name",
	} {
		if got, err := ParseDataset(in); err != nil || got != want {
			t.Errorf("ParseDataset(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"name", "https://example.com/datasets/a/b", "https://huggingface.co/models/a", "a/b/c"} {
		if _, err := ParseDataset(bad); err == nil {
			t.Errorf("ParseDataset(%q) accepted", bad)
		}
	}
}

func TestSniff(t *testing.T) {
	cases := []struct {
		cols []string
		want string
	}{
		{[]string{"instance_id", "repo", "FAIL_TO_PASS", "PASS_TO_PASS"}, "swebench"},
		{[]string{"question_id", "question", "answer", "haystack_sessions"}, "longmemeval"},
		{[]string{"task_id", "Question", "Level", "Final answer"}, "gaia"},
		{[]string{"id", "Question", "Answer"}, "rows"},
		{[]string{"prompt", "ground_truth"}, "rows"},
		{[]string{"image", "label"}, ""},
	}
	for _, c := range cases {
		if got := Sniff(c.cols); got.Importer != c.want || got.Reason == "" {
			t.Errorf("Sniff(%v) = %+v, want %q", c.cols, got, c.want)
		}
	}
}

func TestFetchPagesRowsWithToken(t *testing.T) {
	const total = 230
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		q := r.URL.Query()
		switch r.URL.Path {
		case "/splits":
			fmt.Fprintf(w, `{"splits":[{"dataset":%q,"config":"default","split":"train"},{"dataset":%[1]q,"config":"default","split":"test"}]}`, q.Get("dataset"))
		case "/info":
			fmt.Fprint(w, `{"dataset_info":{"license":"mit","features":{"question":{},"answer":{}},"splits":{"test":{"num_examples":230}}}}`)
		case "/rows":
			off, _ := strconv.Atoi(q.Get("offset"))
			n, _ := strconv.Atoi(q.Get("length"))
			if n > PageRows {
				http.Error(w, `{"error":"length too big"}`, 422)
				return
			}
			type row struct {
				RowIdx int            `json:"row_idx"`
				Row    map[string]any `json:"row"`
			}
			var rows []row
			for i := off; i < off+n && i < total; i++ {
				rows = append(rows, row{i, map[string]any{"question": fmt.Sprintf("q%d <b>", i), "answer": i}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"features":       []map[string]any{{"name": "question"}, {"name": "answer"}},
				"rows":           rows,
				"num_rows_total": total,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "tok", Pause: 1}
	ctx := context.Background()
	splits, err := c.Splits(ctx, "org/qa")
	if err != nil || len(splits) != 2 {
		t.Fatalf("splits %v %v", splits, err)
	}
	info, err := c.Info(ctx, splits[1])
	if err != nil || info.License != "mit" || info.Rows != 230 || strings.Join(info.Columns, ",") != "answer,question" {
		t.Fatalf("info %+v %v", info, err)
	}
	var buf bytes.Buffer
	res, err := c.Fetch(ctx, splits[1], 0, &buf)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if res.Rows != total || len(lines) != total || res.Route.Importer != "rows" {
		t.Fatalf("rows %d lines %d route %+v", res.Rows, len(lines), res.Route)
	}
	if lines[229] != `{"answer":229,"question":"q229 <b>"}` {
		t.Fatalf("last line %s", lines[229])
	}
	for _, a := range auth {
		if a != "Bearer tok" {
			t.Fatalf("auth header %q", a)
		}
	}
	buf.Reset()
	if res, err := c.Fetch(ctx, splits[1], 150, &buf); err != nil || res.Rows != 150 {
		t.Fatalf("max 150: %v %+v", err, res)
	}
}

func TestGatedErrorSaysWhatToDo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"The dataset is gated"}`)
	}))
	defer srv.Close()
	_, err := (&Client{BaseURL: srv.URL}).Splits(context.Background(), "org/gated")
	if err == nil || !strings.Contains(err.Error(), "HF_TOKEN") {
		t.Fatalf("err %v", err)
	}
}
