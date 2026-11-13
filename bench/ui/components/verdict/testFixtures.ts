// Test-only helpers: the committed SYNTHETIC fixtures as typed payloads, plus variants per decision.
import diagnosticsJson from "@/public/fixtures/experiments.diagnostics.json";
import gridJson from "@/public/fixtures/experiments.grid.json";
import verdictJson from "@/public/fixtures/experiments.verdict.json";
import type { DiagnosticsResponse, GridResponse, Verdict, VerdictResponse } from "@/lib/types/bench";

export const verdictFixture = verdictJson as unknown as VerdictResponse;
export const diagnosticsFixture = diagnosticsJson as unknown as DiagnosticsResponse;
export const gridFixture = gridJson as unknown as GridResponse;

/** A copy of the fixture verdict with fields replaced. */
export function verdictWith(overrides: Partial<Verdict>): Verdict {
	return { ...structuredClone(verdictFixture.verdict), ...overrides };
}