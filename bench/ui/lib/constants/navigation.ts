import {
	Boxes,
	Bot,
	Download,
	FlaskConical,
	GitCompareArrows,
	Gavel,
	ListOrdered,
	PackageCheck,
	ReceiptText,
	ScanSearch,
	Server,
	type LucideIcon,
} from "lucide-react";

export interface BenchNavItem {
	to:
		| "/leaderboard"
		| "/suites"
		| "/imports"
		| "/agents"
		| "/workers"
		| "/verdict"
		| "/runs"
		| "/receipts"
		| "/experiments/new"
		| "/triage"
		| "/releases";
	labelKey:
		| "nav.leaderboard"
		| "nav.suites"
		| "nav.imports"
		| "nav.agents"
		| "nav.workers"
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
	{ to: "/imports", labelKey: "nav.imports", icon: Download, testId: "nav-link-imports" },
	{ to: "/agents", labelKey: "nav.agents", icon: Bot, testId: "nav-link-agents" },
	{ to: "/workers", labelKey: "nav.workers", icon: Server, testId: "nav-link-workers" },
	{ to: "/verdict", labelKey: "nav.verdict", icon: Gavel, testId: "nav-link-verdict" },
	{ to: "/runs", labelKey: "nav.runs", icon: GitCompareArrows, testId: "nav-link-runs" },
	{ to: "/receipts", labelKey: "nav.receipts", icon: ReceiptText, testId: "nav-link-receipts" },
	{ to: "/triage", labelKey: "nav.triage", icon: ScanSearch, testId: "nav-link-triage" },
	{ to: "/releases", labelKey: "nav.releases", icon: PackageCheck, testId: "nav-link-releases" },
	{ to: "/experiments/new", labelKey: "nav.newExperiment", icon: FlaskConical, testId: "nav-link-new-experiment" },
];