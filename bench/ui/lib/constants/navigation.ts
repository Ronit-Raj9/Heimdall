import { FlaskConical, GitCompareArrows, Gavel, ReceiptText, type LucideIcon } from "lucide-react";

export interface BenchNavItem {
	to: "/verdict" | "/runs" | "/receipts" | "/experiments/new";
	labelKey: "nav.verdict" | "nav.runs" | "nav.receipts" | "nav.newExperiment";
	icon: LucideIcon;
	testId: string;
}

/** Left navigation. One entry per P0 screen (DESIGN.md 18). Add a screen here and in app/. */
export const BENCH_NAV: readonly BenchNavItem[] = [
	{ to: "/verdict", labelKey: "nav.verdict", icon: Gavel, testId: "nav-link-verdict" },
	{ to: "/runs", labelKey: "nav.runs", icon: GitCompareArrows, testId: "nav-link-runs" },
	{ to: "/receipts", labelKey: "nav.receipts", icon: ReceiptText, testId: "nav-link-receipts" },
	{ to: "/experiments/new", labelKey: "nav.newExperiment", icon: FlaskConical, testId: "nav-link-new-experiment" },
];