import {
	Boxes,
	ClipboardCheck,
	Bot,
	Download,
	FlaskConical,
	GitCompareArrows,
	Gavel,
	Grid3x3,
	Layers,
	ListOrdered,
	PackageCheck,
	ReceiptText,
	ScanSearch,
	Server,
	ShieldCheck,
	TrendingUp,
	type LucideIcon,
} from "lucide-react";

export interface BenchNavItem {
	to:
		| "/leaderboard"
		| "/trends"
		| "/suites"
		| "/imports"
		| "/agents"
		| "/workers"
		| "/verdict"
		| "/runs"
		| "/receipts"
		| "/review"
		| "/experiments/new"
		| "/triage"
		| "/matrix"
		| "/clusters"
		| "/releases"
		| "/monitors"; // Eval integrity area (S2)
	labelKey:
		| "nav.leaderboard"
		| "nav.trends"
		| "nav.suites"
		| "nav.imports"
		| "nav.agents"
		| "nav.workers"
		| "nav.verdict"
		| "nav.runs"
		| "nav.receipts"
		| "nav.review"
		| "nav.newExperiment"
		| "nav.triage"
		| "nav.matrix"
		| "nav.clusters"
		| "nav.releases"
		| "nav.monitors"; // Eval integrity area (S2)
	icon: LucideIcon;
	testId: string;
}

/** Left navigation. One entry per P0 screen (DESIGN.md 18). Add a screen here and in app/. */
export const BENCH_NAV: readonly BenchNavItem[] = [
	{ to: "/leaderboard", labelKey: "nav.leaderboard", icon: ListOrdered, testId: "nav-link-leaderboard" },
	{ to: "/trends", labelKey: "nav.trends", icon: TrendingUp, testId: "nav-link-trends" }, // Index and trends area
	{ to: "/suites", labelKey: "nav.suites", icon: Boxes, testId: "nav-link-suites" },
	{ to: "/imports", labelKey: "nav.imports", icon: Download, testId: "nav-link-imports" },
	{ to: "/agents", labelKey: "nav.agents", icon: Bot, testId: "nav-link-agents" },
	{ to: "/workers", labelKey: "nav.workers", icon: Server, testId: "nav-link-workers" },
	{ to: "/verdict", labelKey: "nav.verdict", icon: Gavel, testId: "nav-link-verdict" },
	{ to: "/runs", labelKey: "nav.runs", icon: GitCompareArrows, testId: "nav-link-runs" },
	{ to: "/receipts", labelKey: "nav.receipts", icon: ReceiptText, testId: "nav-link-receipts" },
	{ to: "/review", labelKey: "nav.review", icon: ClipboardCheck, testId: "nav-link-review" },
	{ to: "/triage", labelKey: "nav.triage", icon: ScanSearch, testId: "nav-link-triage" },
	{ to: "/matrix", labelKey: "nav.matrix", icon: Grid3x3, testId: "nav-link-matrix" }, // Model matrix area
	{ to: "/clusters", labelKey: "nav.clusters", icon: Layers, testId: "nav-link-clusters" },
	{ to: "/releases", labelKey: "nav.releases", icon: PackageCheck, testId: "nav-link-releases" },
	{ to: "/monitors", labelKey: "nav.monitors", icon: ShieldCheck, testId: "nav-link-monitors" }, // Eval integrity area (S2)
	{ to: "/experiments/new", labelKey: "nav.newExperiment", icon: FlaskConical, testId: "nav-link-new-experiment" },
];