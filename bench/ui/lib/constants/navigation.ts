import {
	Boxes,
	Bot,
	FlaskConical,
	GitCompareArrows,
	Gavel,
	ListOrdered,
	PackageCheck,
	ReceiptText,
	ScanSearch,
	type LucideIcon,
} from "lucide-react";

export interface BenchNavItem {
	to: "/leaderboard" | "/suites" | "/agents" | "/verdict" | "/runs" | "/receipts" | "/experiments/new" | "/triage" | "/releases";
	labelKey:
		| "nav.leaderboard"
		| "nav.suites"
		| "nav.agents"
		| "nav.verdict"
		| "nav.runs"
		| "nav.receipts"
		| "nav.newExperiment"
		| "nav.triage"
		| "nav.releases";
	icon: LucideIcon;
	testId: string;
}

/** Left navigation. One entry per P0 screen (DESIGN.md 18). Add a screen here and in app/. */
export const BENCH_NAV: readonly BenchNavItem[] = [
	{ to: "/leaderboard", labelKey: "nav.leaderboard", icon: ListOrdered, testId: "nav-link-leaderboard" },
	{ to: "/suites", labelKey: "nav.suites", icon: Boxes, testId: "nav-link-suites" },
	{ to: "/agents", labelKey: "nav.agents", icon: Bot, testId: "nav-link-agents" },
	{ to: "/verdict", labelKey: "nav.verdict", icon: Gavel, testId: "nav-link-verdict" },
	{ to: "/runs", labelKey: "nav.runs", icon: GitCompareArrows, testId: "nav-link-runs" },
	{ to: "/receipts", labelKey: "nav.receipts", icon: ReceiptText, testId: "nav-link-receipts" },
	{ to: "/triage", labelKey: "nav.triage", icon: ScanSearch, testId: "nav-link-triage" },
	{ to: "/releases", labelKey: "nav.releases", icon: PackageCheck, testId: "nav-link-releases" },
	{ to: "/experiments/new", labelKey: "nav.newExperiment", icon: FlaskConical, testId: "nav-link-new-experiment" },
];