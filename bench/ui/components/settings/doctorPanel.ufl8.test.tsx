import "@/lib/i18n";

import { doctorRequest } from "@/lib/store/apis/doctorApi";
import type { Doctor } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { DoctorResult } from "./doctorPanel";

// The body handlers/health.go getDoctor sends (schemas.Doctor, json tags ok, checks[id,status,detail]).
const wire = JSON.parse(
	'{"mode":"self","ok":false,"checks":[{"id":"docker","status":"pass","detail":"server 27.1"},{"id":"image bench-helper","status":"warn","detail":"missing; run make images"},{"id":"gateway","status":"fail","detail":"gateway not reachable"}]}',
) as Doctor;

describe("doctor panel", () => {
	it("asks the server's own route", () => {
		expect(doctorRequest).toEqual({ url: "/doctor" });
	});

	it("shows every check with its state and what to do", () => {
		const html = renderToStaticMarkup(<DoctorResult result={wire} />);
		expect(html.match(/data-testid="doctor-check"/g)?.length).toBe(3);
		expect(html).toContain('data-status="fail"');
		expect(html).toContain("missing; run make images");
		expect(html).toContain("1 check failed.");
	});

	it("says all passed when nothing failed", () => {
		const html = renderToStaticMarkup(<DoctorResult result={{ ok: true, checks: [{ id: "docker", status: "pass", detail: "" }] }} />);
		expect(html).toContain("All checks passed.");
	});
});