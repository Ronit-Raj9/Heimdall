import "@/lib/i18n";

import { checkKind, PolicyForm, starterPolicy, withKind } from "@/components/govern/policyForm";
import { splitIds } from "@/components/govern/monitorCertifyForm";
import { isValidPolicyFile, normalizePolicyFile, policyPath } from "@/lib/store/apis/governApi";
import { renderToStaticMarkup } from "react-dom/server";
import { mutation, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { freePolicyFile, PolicyEditor } from "./policiesView";

vi.mock("@/lib/store/apis/governApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/governApi")>()),
	useSavePolicyMutation: () => mutation(),
	useValidatePolicyMutation: () => mutation(),
	useDeletePolicyMutation: () => mutation(),
}));

const noop = () => {};

describe("PolicyForm", () => {
	it("renders the starter policy's checks with their rule kinds", () => {
		const html = renderToStaticMarkup(<PolicyForm value={starterPolicy()} onChange={noop} />);
		expect(html).toContain('data-testid="policy-form"');
		expect(html).toContain('data-testid="policy-check-0"');
		expect(html).toContain('data-testid="policy-check-2"');
		expect(html).toContain('data-testid="policy-check-threshold-1"');
		expect(html).not.toContain('data-testid="policy-check-threshold-0"');
		expect(html).toContain("Add rule");
	});

	it("moves a check between rule kinds and keeps the threshold", () => {
		const c = { id: "c", metric: "cost_usd", max: 3 };
		expect(checkKind(c)).toBe("max");
		const min = withKind(c, "min");
		expect(min).toEqual({ id: "c", metric: "cost_usd", min: 3 });
		const rule = withKind(min, "no_regression");
		expect(rule).toEqual({ id: "c", metric: "cost_usd", rule: "no_regression" });
		expect(checkKind(rule)).toBe("no_regression");
	});
});

describe("policy file names and ids", () => {
	it("normalizes and validates file names like the server", () => {
		expect(normalizePolicyFile(" My Policy ")).toBe("my-policy.yaml");
		expect(isValidPolicyFile("strict.yaml")).toBe(true);
		expect(isValidPolicyFile("../x.yaml")).toBe(false);
		expect(isValidPolicyFile("a/b.yaml")).toBe(false);
		expect(policyPath("a b.yaml")).toBe("/policies/a%20b.yaml");
		expect(splitIds("e_1, e_2 e_1")).toEqual(["e_1", "e_2"]);
	});
});

// N6: a new rule set never proposes, or saves over, a file name that is taken.
describe("new rule set file name", () => {
	it("proposes the first free my-release-policy name", () => {
		expect(freePolicyFile([])).toBe("my-release-policy.yaml");
		expect(freePolicyFile(["my-release-policy.yaml"])).toBe("my-release-policy-2.yaml");
		expect(freePolicyFile(["my-release-policy.yaml", "my-release-policy-2.yaml"])).toBe("my-release-policy-3.yaml");
	});

	it("opens the new editor on a free name", () => {
		const html = render(<PolicyEditor isNew taken={["my-release-policy.yaml"]} />);
		expect(html).toContain('value="my-release-policy-2.yaml"');
		expect(html).not.toContain('data-testid="policy-file-taken"');
	});

	it("says a taken name is taken and keeps Save disabled", () => {
		const html = render(<PolicyEditor isNew file="my-release-policy.yaml" taken={["my-release-policy.yaml"]} />);
		expect(html).toContain('data-testid="policy-file-taken"');
		expect(html).toContain("File name taken. Pick another.");
		expect(html).toMatch(/<button[^>]*disabled=""[^>]*data-testid="policy-save"|<button[^>]*data-testid="policy-save"[^>]*disabled=""/);
	});
});