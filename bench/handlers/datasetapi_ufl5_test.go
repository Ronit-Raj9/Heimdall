package handlers

import (
	"encoding/json"
	"strings"
	"testing"
)

// The Rows section of a benchmark's Data tab (round 5, L5) sends these
// bodies, byte for byte what rowRequest in
// ui/components/suites/datasetRows.tsx builds (its test checks the same
// strings): add a row, replace it, then delete it, and read one version.
const (
	webAddRowBody     = `{"tasks":[{"input":"What is 2+2?","id":"q-sum","expected":["4","four"]}],"mode":"add","source":"web"}`
	webReplaceRowBody = `{"tasks":[{"input":"What is 3+3?","id":"q-sum","expected":["6"]}],"mode":"upsert","source":"web"}`
)

func TestDatasetRowsFromTheWebForm(t *testing.T) {
	c := newDatasetSyncClient(t)
	newQASuite(t, c, "zz-web-rows")
	base := "/suites/zz-web-rows/dataset"
	code, body := c.do(t, "POST", base+"/tasks", []byte(webAddRowBody), nil)
	var res DatasetTasksResult
	if code/100 != 2 || json.Unmarshal(body, &res) != nil || len(res.Added) != 1 || res.Added[0] != "q-sum" || len(res.Problems) != 0 {
		t.Fatalf("add: %d %s", code, body)
	}
	code, body = c.do(t, "GET", base+"/tasks?offset=0&limit=50", nil, nil)
	var list DatasetTaskList
	if code != 200 || json.Unmarshal(body, &list) != nil || list.Total != 1 || len(list.Tasks) != 1 || !list.Tasks[0].FromRows {
		t.Fatalf("list: %d %s", code, body)
	}
	code, body = c.do(t, "POST", base+"/tasks", []byte(webReplaceRowBody), nil)
	res = DatasetTasksResult{}
	if code/100 != 2 || json.Unmarshal(body, &res) != nil || len(res.Updated) != 1 || res.Version == nil {
		t.Fatalf("replace: %d %s", code, body)
	}
	// The same row again under mode add is refused with a problem, not written twice.
	code, body = c.do(t, "POST", base+"/tasks", []byte(webAddRowBody), nil)
	res = DatasetTasksResult{}
	if json.Unmarshal(body, &res) != nil || (code/100 == 2 && len(res.Problems) == 0) {
		t.Fatalf("add a taken id: %d %s", code, body)
	}
	code, body = c.do(t, "GET", base+"/versions/2", nil, nil)
	if code != 200 || !strings.Contains(string(body), `"updated":["q-sum"]`) {
		t.Fatalf("version 2: %d %s", code, body)
	}
	if code, body = c.do(t, "DELETE", base+"/tasks/q-sum", nil, nil); code/100 != 2 {
		t.Fatalf("delete: %d %s", code, body)
	}
	code, body = c.do(t, "GET", base+"/tasks?offset=0&limit=50", nil, nil)
	list = DatasetTaskList{}
	if code != 200 || json.Unmarshal(body, &list) != nil || list.Total != 0 {
		t.Fatalf("list after delete: %d %s", code, body)
	}
}
