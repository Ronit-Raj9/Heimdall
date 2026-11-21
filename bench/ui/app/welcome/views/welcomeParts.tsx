import type { ModelRef, ProviderId } from "@/components/models/modelPicker";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import type { AgentSummary, ExperimentStatus, SuiteSummary } from "@/lib/types/bench";
import "@/lib/types/wizardI18n";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import {
	Bot,
	Brain,
	Check,
	Code2,
	Globe,
	LibraryBig,
	MessagesSquare,
	Mic,
	Monitor,
	Search,
	ShieldCheck,
	SquareTerminal,
	Users,
	Wrench,
	type LucideIcon,
} from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { categoryOf, initialsOf, prettyName, STARTER_SUITE, suiteReady, type SuiteCategory } from "./welcome.utils";

export const CATEGORY_ICON: Record<SuiteCategory, LucideIcon> = {
	coding: Code2,
	terminal: SquareTerminal,
	memory: Brain,
	computer: Monitor,
	browser: Globe,
	voice: Mic,
	support: MessagesSquare,
	research: Search,
	safety: ShieldCheck,
	multiagent: Users,
	tools: Wrench,
};

/** Soft tint per family so the grid reads at a glance. */
const CATEGORY_TINT: Record<SuiteCategory, string> = {
	coding: "bg-sky-500/10 text-sky-600 dark:text-sky-400",
	terminal: "bg-zinc-500/10 text-zinc-700 dark:text-zinc-300",
	memory: "bg-violet-500/10 text-violet-600 dark:text-violet-400",
	computer: "bg-indigo-500/10 text-indigo-600 dark:text-indigo-400",
	browser: "bg-cyan-500/10 text-cyan-600 dark:text-cyan-400",
	voice: "bg-pink-500/10 text-pink-600 dark:text-pink-400",
	support: "bg-amber-500/10 text-amber-600 dark:text-amber-400",
	research: "bg-teal-500/10 text-teal-600 dark:text-teal-400",
	safety: "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
	multiagent: "bg-orange-500/10 text-orange-600 dark:text-orange-400",
	tools: "bg-slate-500/10 text-slate-600 dark:text-slate-300",
};

export function IconTile({ icon: Icon, className, size = "md" }: { icon: LucideIcon; className?: string; size?: "sm" | "md" | "lg" }) {
	return (
		<span
			className={cn(
				"flex shrink-0 items-center justify-center rounded-lg",
				size === "sm" && "size-7",
				size === "md" && "size-9",
				size === "lg" && "size-12 rounded-xl",
				className,
			)}
			aria-hidden="true"
		>
			<Icon className={cn(size === "lg" ? "size-6" : size === "sm" ? "size-3.5" : "size-[18px]")} />
		</span>
	);
}

export function Pill({ tone, children, pulse }: { tone: "ok" | "warn" | "info" | "muted"; children: ReactNode; pulse?: boolean }) {
	return (
		<span
			className={cn(
				"inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium",
				tone === "ok" && "bg-chart-success/15 text-chart-success-ink",
				tone === "warn" && "bg-chart-warning/15 text-chart-warning-ink",
				tone === "info" && "bg-chart-info/15 text-chart-info-ink",
				tone === "muted" && "bg-muted text-muted-foreground",
			)}
		>
			<span
				className={cn(
					"size-1.5 rounded-full",
					tone === "ok" && "bg-chart-success",
					tone === "warn" && "bg-chart-warning",
					tone === "info" && "bg-chart-info",
					tone === "muted" && "bg-muted-foreground/50",
					pulse && "animate-pulse",
				)}
			/>
			{children}
		</span>
	);
}

/** A selectable card with a check badge and hover lift. */
export function Tile({ selected, onPick, testId, children }: { selected: boolean; onPick(): void; testId: string; children: ReactNode }) {
	return (
		<button
			type="button"
			role="radio"
			aria-checked={selected}
			onClick={onPick}
			className={cn(
				"group bg-card relative flex min-w-0 flex-col gap-2.5 rounded-xl border p-4 text-left transition-all duration-200",
				"hover:border-primary/40 hover:-translate-y-0.5 hover:shadow-md focus-visible:ring-ring/60 focus-visible:ring-2 focus-visible:outline-none",
				selected && "border-primary bg-primary/[0.04] ring-primary/25 shadow-sm ring-2",
			)}
			data-testid={testId}
			data-selected={selected}
		>
			<span
				className={cn(
					"bg-primary text-primary-foreground absolute top-3 right-3 flex size-5 items-center justify-center rounded-full transition-all duration-200",
					selected ? "scale-100 opacity-100" : "scale-50 opacity-0",
				)}
				aria-hidden="true"
			>
				<Check className="size-3" />
			</span>
			{children}
		</button>
	);
}

export function SuiteTile({ suite, selected, onPick }: { suite: SuiteSummary; selected: boolean; onPick(): void }) {
	const { t } = useTranslation("wizard");
	const { t: tw } = useTranslation("welcome");
	const cat = categoryOf(suite);
	const ready = suiteReady(suite);
	return (
		<Tile selected={selected} onPick={onPick} testId={`welcome-suite-${suite.id}`}>
			<span className="flex items-center gap-3 pr-6">
				<IconTile icon={CATEGORY_ICON[cat]} className={CATEGORY_TINT[cat]} />
				<span className="flex min-w-0 flex-col">
					<span className="truncate text-sm font-semibold">{prettyName(suite.id)}</span>
					<span className="text-muted-foreground text-xs">{t(`category.${cat}`)}</span>
				</span>
			</span>
			<span className="text-muted-foreground line-clamp-2 min-h-8 text-xs leading-4">{suite.description || t(`categoryHint.${cat}`)}</span>
			<span className="mt-auto flex flex-wrap items-center gap-1.5">
				<Pill tone={ready ? "ok" : "warn"}>{ready ? t("ready") : t("notReady")}</Pill>
				{suite.id === STARTER_SUITE && <Pill tone="info">{tw("suite.recommended")}</Pill>}
				<span className="text-muted-foreground ml-auto text-[11px] tabular-nums">{tw("suite.tasks", { count: suite.task_count })}</span>
			</span>
		</Tile>
	);
}

export function CatalogTile() {
	const { t } = useTranslation("wizard");
	return (
		<Link
			to="/catalog"
			className="text-muted-foreground hover:border-primary/40 hover:text-foreground flex min-h-36 flex-col items-center justify-center gap-2 rounded-xl border border-dashed p-4 text-center transition-all duration-200 hover:-translate-y-0.5 hover:shadow-md"
			data-testid="welcome-catalog-tile"
		>
			<IconTile icon={LibraryBig} className="bg-muted" />
			<span className="text-foreground text-sm font-semibold">{t("catalogTile")}</span>
			<span className="text-xs">{t("catalogTileHint")}</span>
		</Link>
	);
}

/** Category filter chips; only families present in the org are shown. */
export function CategoryChips({ present, value, onChange }: { present: SuiteCategory[]; value: SuiteCategory | null; onChange(c: SuiteCategory | null): void }) {
	const { t } = useTranslation("wizard");
	if (present.length < 2) return null;
	const chip = (active: boolean) =>
		cn(
			"inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium transition-colors",
			active ? "border-primary bg-primary text-primary-foreground" : "hover:bg-accent text-muted-foreground",
		);
	return (
		<div className="flex flex-wrap gap-1.5" data-testid="welcome-categories">
			<button type="button" className={chip(value === null)} onClick={() => onChange(null)}>
				{t("all")}
			</button>
			{present.map((c) => {
				const Icon = CATEGORY_ICON[c];
				return (
					<button key={c} type="button" className={chip(value === c)} onClick={() => onChange(value === c ? null : c)} data-testid={`welcome-category-${c}`}>
						<Icon className="size-3.5" aria-hidden="true" />
						{t(`category.${c}`)}
					</button>
				);
			})}
		</div>
	);
}

const AGENT_TINT = [
	"from-orange-400 to-rose-500",
	"from-sky-400 to-indigo-500",
	"from-emerald-400 to-teal-500",
	"from-violet-400 to-fuchsia-500",
	"from-amber-400 to-orange-500",
	"from-cyan-400 to-blue-500",
];

function hashOf(s: string): number {
	let h = 0;
	for (const ch of s) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
	return h;
}

export function Monogram({ name, className }: { name: string; className?: string }) {
	return (
		<span
			className={cn(
				"flex size-10 shrink-0 items-center justify-center rounded-xl bg-gradient-to-br text-sm font-bold tracking-tight text-white shadow-sm",
				AGENT_TINT[hashOf(name) % AGENT_TINT.length],
				className,
			)}
			aria-hidden="true"
		>
			{initialsOf(name)}
		</span>
	);
}

export function AgentTile({ agent, selected, onPick }: { agent: AgentSummary; selected: boolean; onPick(): void }) {
	const { t } = useTranslation("wizard");
	return (
		<Tile selected={selected} onPick={onPick} testId={`welcome-agent-${agent.id}`}>
			<span className="flex items-center gap-3 pr-6">
				{agent.scripted_kind ? <IconTile icon={Bot} className="bg-muted text-muted-foreground size-10 rounded-xl" /> : <Monogram name={agent.id} />}
				<span className="flex min-w-0 flex-col">
					<span className="truncate text-sm font-semibold">{prettyName(agent.id)}</span>
					<span className="text-muted-foreground truncate font-mono text-[11px]">{agent.id}</span>
				</span>
			</span>
			<span className="mt-auto flex flex-wrap items-center gap-1.5">
				{agent.model && (
					<span className="bg-muted inline-flex max-w-full items-center gap-1 truncate rounded-full px-2 py-0.5 font-mono text-[11px]" title={t("defaultModel")}>
						{agent.model}
					</span>
				)}
				{agent.scripted_kind && <Pill tone="muted">{t("scripted")}</Pill>}
			</span>
		</Tile>
	);
}

const PROVIDER_STYLE: Partial<Record<ProviderId, { label: string; cls: string }>> = {
	openai: { label: "AI", cls: "bg-zinc-900 text-white dark:bg-white dark:text-zinc-900" },
	anthropic: { label: "A", cls: "bg-[#d97757] text-white" },
	gemini: { label: "G", cls: "bg-gradient-to-br from-sky-500 to-violet-500 text-white" },
	openrouter: { label: "OR", cls: "bg-indigo-600 text-white" },
	groq: { label: "GQ", cls: "bg-orange-600 text-white" },
	sarvam: { label: "S", cls: "bg-emerald-600 text-white" },
};

export function ProviderMark({ provider, className }: { provider: ProviderId; className?: string }) {
	const s = PROVIDER_STYLE[provider] ?? { label: initialsOf(provider), cls: "bg-muted text-foreground" };
	return (
		<span className={cn("flex size-7 shrink-0 items-center justify-center rounded-lg text-[11px] font-bold", s.cls, className)} aria-hidden="true">
			{s.label}
		</span>
	);
}

/**
 * Provider cards with key status and one-click suggested models, above the full ModelPicker. It
 * reads the same GET /models the picker reads, so it adds no request.
 */
export function ModelQuickPick({ value, onChange }: { value: ModelRef | null; onChange(v: ModelRef): void }) {
	const { t } = useTranslation("wizard");
	const models = useGetModelsQuery();
	const catalog = models.data?.providers ?? [];
	const keys = models.data?.keys ?? [];
	if (catalog.length === 0) return null;
	const statusOf = (p: ProviderId): "ok" | "warn" | "muted" => {
		const ks = keys.filter((k) => k.provider === p);
		if (ks.some((k) => k.status === "ok" || k.status === "untested")) return "ok";
		if (ks.length > 0) return "warn";
		return "muted";
	};
	const ordered = [...catalog].sort((a, b) => Number(statusOf(b.id) === "ok") - Number(statusOf(a.id) === "ok")).slice(0, 6);
	const active = value?.provider ?? ordered[0]?.id;
	const key = keys.find((k) => k.provider === active && k.models.length > 0);
	const info = catalog.find((p) => p.id === active);
	const suggestions = (
		key?.models ??
		(info?.models ?? [])
			.filter((m) => m.purposes.includes("agent"))
			.sort((a, b) => a.rank - b.rank)
			.map((m) => m.id)
	).slice(0, 4);
	return (
		<div className="flex flex-col gap-4" data-testid="welcome-model-quick">
			<div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
				{ordered.map((p) => {
					const st = statusOf(p.id);
					const on = p.id === active;
					return (
						<button
							key={p.id}
							type="button"
							onClick={() => onChange({ provider: p.id, model: p.id === value?.provider ? value.model : "" })}
							className={cn(
								"bg-card flex min-w-0 items-center gap-2.5 rounded-xl border p-3 text-left transition-all duration-200 hover:-translate-y-0.5 hover:shadow-md",
								on && "border-primary ring-primary/25 ring-2",
							)}
							aria-pressed={on}
							data-testid={`welcome-provider-${p.id}`}
						>
							<ProviderMark provider={p.id} />
							<span className="flex min-w-0 flex-col gap-0.5">
								<span className="truncate text-sm font-medium">{p.name}</span>
								<Pill tone={st}>{st === "ok" ? t("keyReady") : st === "warn" ? t("keyBroken") : t("keyMissing")}</Pill>
							</span>
						</button>
					);
				})}
			</div>
			{suggestions.length > 0 && active && (
				<div className="flex flex-wrap items-center gap-1.5">
					<span className="text-muted-foreground mr-1 text-xs font-medium">{t("suggested")}</span>
					{suggestions.map((m) => (
						<button
							key={m}
							type="button"
							onClick={() => onChange({ provider: active, model: m, keyId: value?.provider === active ? value.keyId : undefined })}
							className={cn(
								"rounded-full border px-2.5 py-1 font-mono text-xs transition-colors",
								value?.model === m && value.provider === active ? "border-primary bg-primary text-primary-foreground" : "hover:bg-accent",
							)}
						>
							{m}
						</button>
					))}
				</div>
			)}
		</div>
	);
}

type SlotState = "done" | "failed" | "running" | "pending";

/** One cell per run: finished, failed, running (pulsing) and waiting. */
export function slotCells(s: ExperimentStatus["slots"]): SlotState[] {
	const total = Math.max(0, s.total);
	const done = Math.max(0, s.done - s.failed);
	const cells: SlotState[] = [];
	for (let i = 0; i < total; i++) {
		if (i < done) cells.push("done");
		else if (i < done + s.failed) cells.push("failed");
		else if (i < done + s.failed + s.running) cells.push("running");
		else cells.push("pending");
	}
	return cells;
}

/** Live view of a running experiment: pulsing header, progress bar, per-run cells and the latest change. */
export function LiveProgress({ status }: { status: ExperimentStatus }) {
	const { t } = useTranslation("wizard");
	const { slots } = status;
	const finished = slots.done;
	const pct = slots.total > 0 ? Math.round((finished / slots.total) * 100) : 0;
	const sealed = status.state === "analyzing" || status.state === "done";
	const latest = useLatestLine(status);
	const cells = slotCells(slots);
	const dot: Record<SlotState, string> = {
		done: "bg-chart-success",
		failed: "bg-chart-error",
		running: "bg-chart-info animate-pulse",
		pending: "bg-muted-foreground/20",
	};
	return (
		<div className="bg-muted/30 flex flex-col gap-4 rounded-xl border p-4 md:p-5" data-testid="welcome-live" data-state={status.state}>
			<div className="flex items-center gap-3">
				<span className="relative flex size-3" aria-hidden="true">
					{!sealed && <span className="bg-chart-info absolute inline-flex size-full animate-ping rounded-full opacity-60" />}
					<span className={cn("relative inline-flex size-3 rounded-full", sealed ? "bg-chart-success" : "bg-chart-info")} />
				</span>
				<span className="text-sm font-semibold">{slots.running > 0 || finished > 0 ? t("live.running") : t("live.queued")}</span>
				<span className="text-muted-foreground ml-auto text-sm tabular-nums">{t("live.done", { done: finished, total: slots.total })}</span>
			</div>
			<div className="bg-muted h-2 overflow-hidden rounded-full" role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
				<div className="bg-primary h-full rounded-full transition-[width] duration-700 ease-out" style={{ width: `${pct}%` }} />
			</div>
			{cells.length > 0 && (
				<div className="flex flex-wrap gap-1" aria-label={t("live.slots")} data-testid="welcome-live-slots">
					{cells.map((c, i) => (
						<span key={i} className={cn("size-3 rounded-[3px] transition-colors duration-500", dot[c])} title={t(`live.legend${c[0].toUpperCase()}${c.slice(1)}` as "live.legendDone")} />
					))}
				</div>
			)}
			<p key={latest} className="text-muted-foreground animate-in fade-in slide-in-from-left-1 truncate font-mono text-xs duration-300" data-testid="welcome-live-latest">
				{latest}
			</p>
		</div>
	);
}

/** The latest event line, derived from what changed between two polls. */
function useLatestLine(status: ExperimentStatus): string {
	const { t } = useTranslation("wizard");
	const { slots, state } = status;
	const prev = useRef(slots);
	const [line, setLine] = useState<string | null>(null);
	useEffect(() => {
		const p = prev.current;
		if (slots.failed > p.failed) setLine(t("live.latestFailed", { failed: slots.failed }));
		prev.current = slots;
	}, [slots, t]);
	if (state === "analyzing" || state === "done") return t("live.latestSealed");
	if (line && slots.failed > 0 && slots.running === 0) return line;
	if (slots.running === 0 && slots.done === 0) return t("live.latestStarting");
	return t("live.latestRunning", { running: slots.running, done: slots.done });
}

/** A vs B pass-rate bars from the naive scoreboard. */
export function ArmBars({ naive, winner }: { naive: Record<string, { num: number; den: number }>; winner?: string }) {
	const { t } = useTranslation("wizard");
	const { t: tw } = useTranslation("welcome");
	const arms = Object.entries(naive);
	if (arms.length === 0) return null;
	return (
		<div className="flex flex-col gap-3" data-testid="welcome-arm-bars">
			{arms.map(([arm, r], i) => {
				const pct = r.den > 0 ? Math.round((r.num / r.den) * 100) : 0;
				const label = arm === "A" ? t("armA") : arm === "B" ? t("armB") : arm;
				return (
					<div key={arm} className="flex flex-col gap-1">
						<div className="flex items-baseline gap-2 text-sm">
							<span className="bg-background flex size-5 items-center justify-center rounded border font-mono text-[11px] font-semibold">{arm}</span>
							<span className="font-medium">{label}</span>
							<span className="text-muted-foreground ml-auto text-xs tabular-nums">{tw("verdict.passRate", { num: r.num, den: r.den })}</span>
							<span className="w-10 text-right text-sm font-semibold tabular-nums">{pct}%</span>
						</div>
						<div className="bg-background/70 h-2.5 overflow-hidden rounded-full border">
							<div
								className={cn("animate-in slide-in-from-left h-full rounded-full duration-700", arm === winner ? "bg-chart-success" : i === 0 ? "bg-muted-foreground/40" : "bg-primary")}
								style={{ width: `${pct}%` }}
							/>
						</div>
					</div>
				);
			})}
		</div>
	);
}
