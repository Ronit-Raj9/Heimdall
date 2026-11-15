package traceexport

import "testing"

func TestPayloadAttrs(t *testing.T) {
	cases := []struct {
		name      string
		payloads  bool
		text, sum string
		want      map[string]string
		absent    []string
	}{
		{"whole value", false, "abc", hexSHA256("abc"),
			map[string]string{"heimdall.tool.args_sha256": hexSHA256("abc"), "heimdall.tool.args_bytes": "3"},
			[]string{"heimdall.tool.args_clipped", "gen_ai.tool.call.arguments"}},
		{"clipped in the ledger", false, "ab", hexSHA256("abc"),
			map[string]string{"heimdall.tool.args_clipped": "true"},
			[]string{"heimdall.tool.args_bytes"}},
		{"no hash recorded", false, "", "", nil,
			[]string{"heimdall.tool.args_sha256", "heimdall.tool.args_bytes", "heimdall.tool.args_clipped"}},
		{"payloads", true, `{"password":"pw-123456"}`, "", map[string]string{"gen_ai.tool.call.arguments": `{"password":"[redacted]"}`}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := &builder{opts: Options{Payloads: c.payloads, PayloadCap: DefaultPayloadCap}}
			a := attrs{}
			b.payloadAttrs(&a, "args", c.text, c.sum)
			for k, v := range c.want {
				wantAttr(t, a, k, v)
			}
			for _, k := range c.absent {
				if _, ok := Attr(a, k); ok {
					t.Fatalf("%s present", k)
				}
			}
		})
	}
}

func TestToolType(t *testing.T) {
	if toolType("external") != "extension" || toolType("mcp") != "function" || toolType("http") != "function" {
		t.Fatal("tool types")
	}
}
