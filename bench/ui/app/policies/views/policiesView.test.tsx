import "@/lib/i18n";

import { checkKind, PolicyForm, starterPolicy, withKind } from "@/components/govern/policyForm";
import { splitIds } from "@/components/govern/monitorCertifyForm";
import { isValidPolicyFile, normalizePolicyFile, policyPath } from "@/lib/store/apis/governApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

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
