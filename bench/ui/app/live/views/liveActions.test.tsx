import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { liveInitParams } from "./liveActions";

const v = { url: "", token_env: "", baseline_key: "vk-a", candidate_key: "vk-b", tag_header: "x-bf-lh-agent-version", baseline_tag: "", candidate_tag: "" };

describe("liveInitParams", () => {
	it("sends the keys split", () => {
		expect(liveInitParams("keys", v, false)).toEqual({ force: false, baseline_key: "vk-a", candidate_key: "vk-b" });
	});
	it("needs both tag values", () => {
		expect(liveInitParams("tag", v, false)).toBeNull();
		expect(liveInitParams("tag", { ...v, baseline_tag: "v1", candidate_tag: "v2" }, true)).toEqual({
			force: true,
			tag_header: "x-bf-lh-agent-version",
			baseline_tag: "v1",
			candidate_tag: "v2",
		});
	});
	it("writes a template when deciding later, with the optional gateway fields trimmed", () => {
		expect(liveInitParams("later", { ...v, url: " http://gw:8080 ", token_env: "BF_TOKEN" }, false)).toEqual({
			force: false,
			url: "http://gw:8080",
			token_env: "BF_TOKEN",
		});
	});
});
