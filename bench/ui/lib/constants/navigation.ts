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
import { Inbox } from "lucide-react"; // Failure inbox area
import { ArrowRightLeft } from "lucide-react"; // W6 model migration
import { Rocket } from "lucide-react"; // First workflow area
import { Scale } from "lucide-react"; // W8 judge calibration
import { Activity } from "lucide-react"; // W5 drift canary
import { Radio } from "lucide-react"; // X6 live scoring

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
		| "/migration" // W6 model migration
		| "/clusters"
		| "/inbox" // Failure inbox area
		| "/quickstart" // First workflow area
		| "/calibration" // W8 judge calibration
		| "/canaries" // W5 drift canary
		| "/live" // X6 live scoring
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
		| "nav.migration" // W6 model migration
		| "nav.clusters"
		| "nav.inbox" // Failure inbox area
		| "nav.quickstart" // First workflow area
		| "nav.calibration" // W8 judge calibration
		| "nav.canaries" // W5 drift canary
		| "nav.live" // X6 live scoring
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
	{ to: "/migration", labelKey: "nav.migration", icon: ArrowRightLeft, testId: "nav-link-migration" }, // W6 model migration
	{ to: "/clusters", labelKey: "nav.clusters", icon: Layers, testId: "nav-link-clusters" },
	{ to: "/inbox", labelKey: "nav.inbox", icon: Inbox, testId: "nav-link-inbox" }, // Failure inbox area
	{ to: "/quickstart", labelKey: "nav.quickstart", icon: Rocket, testId: "nav-link-quickstart" }, // First workflow area
	{ to: "/calibration", labelKey: "nav.calibration", icon: Scale, testId: "nav-link-calibration" }, // W8 judge calibration
	{ to: "/canaries", labelKey: "nav.canaries", icon: Activity, testId: "nav-link-canaries" }, // W5 drift canary
	{ to: "/live", labelKey: "nav.live", icon: Radio, testId: "nav-link-live" }, // X6 live scoring
	{ to: "/releases", labelKey: "nav.releases", icon: PackageCheck, testId: "nav-link-releases" },
	{ to: "/monitors", labelKey: "nav.monitors", icon: ShieldCheck, testId: "nav-link-monitors" }, // Eval integrity area (S2)
	{ to: "/experiments/new", labelKey: "nav.newExperiment", icon: FlaskConical, testId: "nav-link-new-experiment" },
];