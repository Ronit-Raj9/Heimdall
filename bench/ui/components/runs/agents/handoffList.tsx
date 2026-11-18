import { clipText } from "@/components/runs/stepTimeline.utils";
import { ToneBadge } from "@/components/suites/suiteBadges";
import { shortHash } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { AgentHandoff, HandoffFact } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { AlertTriangle, ArrowRight, ChevronRight, CircleCheck, CircleDashed, CirclePlus, type LucideIcon } from "lucide-react";
import { useId, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { AgentId, StrengthBadge } from "./agentBadges";
import {
	FACT_TONE,
	factState,
	formatBytes,
	knownEdgeKind,
	lineDiff,
	safePayload,
	sourceStrength,
	timeOfDay,
	type DiffLine,
	type FactState,
} from "./agents.utils";

export interface HandoffListProps {
	handoffs: readonly AgentHandoff[];
	arm: string;
	/** Handoff indexes open at first render. */
	initialExpanded?: readonly number[];
}

/**
 * Every spawn, handoff, message, claim and return in time order: who to whom, when, how big, the
 * payload hash, whether it arrived altered, and which planted facts survived. A row opens to the
 * payload as sent against as delivered, line by line. Payloads, facts, ids and refs are agent
 * text: React text nodes only.
 */
export function HandoffList({ handoffs, arm, initialExpanded = [] }: HandoffListProps) {
	const { t } = useTranslation("bench");
	const [open, setOpen] = useState<ReadonlySet<number>>(() => new Set(initialExpanded));
	if (handoffs.length === 0) {
		return (
			<p
				className="text-muted-foreground rounded-md border border-dashed px-3 py-4 text-center text-xs"
				data-testid={`agents-handoffs-${arm}-empty`}
			>
				{t("runs.agents.handoffs.empty")}
			</p>
		);
	}
	const toggle = (index: number) =>
		setOpen((prev) => {
			const next = new Set(prev);
			if (next.has(index)) next.delete(index);
			else next.add(index);
			return next;
		});
	return (
		<ol
			className="bg-card divide-border/70 flex min-w-0 flex-col divide-y rounded-lg border shadow-(--shadow-card)"
			aria-label={t("runs.agents.handoffs.label", { arm })}
			data-testid={`agents-handoffs-${arm}`}
		>
			{handoffs.map((handoff) => (
				<HandoffRow key={handoff.index} handoff={handoff} arm={arm} open={open.has(handoff.index)} onToggle={() => toggle(handoff.index)} />
			))}
		</ol>
	);
}

function HandoffRow({ handoff, arm, open, onToggle }: { handoff: AgentHandoff; arm: string; open: boolean; onToggle: () => void }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const panelId = useId();
	const kind = knownEdgeKind(handoff.kind);
	const time = timeOfDay(handoff.at);
	const testId = `agents-handoff-${arm}-${handoff.index}`;
	const lost = (handoff.facts ?? []).some((fact) => factState(fact) === "lost");
	return (
		<li
			className={cn("flex min-w-0 flex-col text-xs", (handoff.altered || lost) && "shadow-[inset_2px_0_0_var(--chart-warning)]")}
			data-testid={testId}
			data-altered={Boolean(handoff.altered)}
		>
			<button
				type="button"
				aria-expanded={open}
				aria-controls={panelId}
				onClick={onToggle}
				className="hover:bg-muted/40 focus-visible:ring-ring/50 flex min-w-0 cursor-pointer flex-wrap items-center gap-x-2 gap-y-1 px-3 py-2 text-left transition-colors focus-visible:ring-[3px] focus-visible:outline-none"
				data-testid={`${testId}-toggle`}
			>
				<ChevronRight
					className={cn("text-muted-foreground size-3.5 shrink-0 transition-transform duration-150", open && "rotate-90")}
					aria-hidden="true"
				/>
				<span className="text-muted-foreground w-6 shrink-0 font-mono tabular-nums">#{handoff.index}</span>
				<ToneBadge tone="neutral" testId={`${testId}-kind`}>
					{kind ? t(`runs.agents.edge.${kind}`) : clipText(handoff.kind, 20)}
				</ToneBadge>
				<span className="inline-flex min-w-0 items-center gap-1.5">
					<AgentId id={handoff.from} className="text-[11px]" />
					<ArrowRight className="text-muted-foreground size-3 shrink-0" aria-hidden="true" />
					<span className="sr-only">{t("runs.agents.handoffs.to")}</span>
					{handoff.to ? (
						<AgentId id={handoff.to} className="text-[11px]" />
					) : (
						<span className="text-muted-foreground italic">{t("runs.agents.handoffs.noRecipient")}</span>
					)}
				</span>
				<span className="text-muted-foreground font-mono tabular-nums">{time ?? t("runs.agents.handoffs.noTime")}</span>
				<span className="text-muted-foreground tabular-nums">{formatBytes(handoff.bytes, locale)}</span>
				{handoff.payload_sha256 && (
					<span className="text-muted-foreground font-mono" title={clipText(handoff.payload_sha256, 100)} data-testid={`${testId}-sha`}>
						{shortHash(clipText(handoff.payload_sha256, 100))}
					</span>
				)}
				{handoff.altered && (
					<ToneBadge tone="warn" icon={AlertTriangle} hint={t("runs.agents.handoffs.alteredHint")} testId={`${testId}-altered`}>
						{t("runs.agents.handoffs.altered")}
					</ToneBadge>
				)}
				<StrengthBadge strength={sourceStrength(handoff.source)} source={handoff.source} testId={`${testId}-source`} />
			</button>
			{handoff.facts && handoff.facts.length > 0 && <FactChips facts={handoff.facts} testId={`${testId}-facts`} />}
			{open && (
				<div id={panelId} className="flex min-w-0 flex-col gap-2 px-3 pt-1 pb-3 pl-9" data-testid={`${testId}-panel`}>
					{handoff.ref && (
						<p className="text-muted-foreground">
							{t("runs.agents.handoffs.ref")} <span className="text-foreground font-mono break-all">{clipText(handoff.ref, 160)}</span>
						</p>
					)}
					<PayloadDiff handoff={handoff} testId={`${testId}-payload`} />
				</div>
			)}
		</li>
	);
}

const FACT_ICON: Record<FactState, LucideIcon> = { kept: CircleCheck, lost: AlertTriangle, added: CirclePlus, absent: CircleDashed };

/** Each planted fact and what became of it on this edge: kept, lost on the way (a warning), added on the way, or never sent. */
function FactChips({ facts, testId }: { facts: readonly HandoffFact[]; testId: string }) {
	const { t } = useTranslation("bench");
	return (
		<ul className="flex flex-wrap items-center gap-1.5 px-3 pb-2 pl-9" aria-label={t("runs.agents.facts.label")} data-testid={testId}>
			{facts.map((fact, i) => {
				const state = factState(fact);
				const hint = `${t(`runs.agents.facts.sent.${fact.sent ? "yes" : "no"}`)}, ${t(`runs.agents.facts.delivered.${fact.delivered ? "yes" : "no"}`)}`;
				return (
					// Facts can repeat, so the position is part of the key.
					<li key={`${i}-${fact.fact}`} data-testid={`${testId}-${i}`} data-state={state}>
						<ToneBadge tone={FACT_TONE[state]} icon={FACT_ICON[state]} hint={hint} className="max-w-[22rem]">
							<span className="truncate font-normal">{clipText(fact.fact, 80)}</span>
							<span className="border-l border-current/30 pl-1">{t(`runs.agents.facts.state.${state}`)}</span>
						</ToneBadge>
					</li>
				);
			})}
		</ul>
	);
}

const DIFF_CLASS: Record<DiffLine["op"], string> = {
	same: "",
	del: "bg-chart-error/10 text-chart-error-ink",
	add: "bg-chart-success/10 text-chart-success-ink",
};
const DIFF_MARK: Record<DiffLine["op"], string> = { same: " ", del: "-", add: "+" };

/** Sent against delivered, line by line; one block when they match; "not recorded" for whichever side is missing. */
function PayloadDiff({ handoff, testId }: { handoff: AgentHandoff; testId: string }) {
	const { t } = useTranslation("bench");
	const sent = handoff.sent === undefined ? undefined : safePayload(handoff.sent);
	const delivered = handoff.delivered === undefined ? undefined : safePayload(handoff.delivered);
	const lines = useMemo(() => (sent !== undefined && delivered !== undefined ? lineDiff(sent, delivered) : []), [sent, delivered]);

	if (sent === undefined && delivered === undefined) {
		return (
			<p className="text-muted-foreground rounded-sm border border-dashed px-3 py-3 text-center" data-testid={`${testId}-none`}>
				{t("runs.agents.handoffs.notRecorded")}
			</p>
		);
	}
	if (sent === undefined || delivered === undefined) {
		const present = (sent ?? delivered)!;
		return (
			<div className="flex flex-col gap-1" data-testid={`${testId}-one`}>
				<span className="text-muted-foreground">
					{sent === undefined ? t("runs.agents.handoffs.onlyDelivered") : t("runs.agents.handoffs.onlySent")}
				</span>
				<PayloadBlock lines={splitPlain(present)} />
			</div>
		);
	}
	const same = lines.every((line) => line.op === "same");
	return (
		<div className="flex flex-col gap-1" data-testid={same ? `${testId}-same` : `${testId}-diff`}>
			<span className="text-muted-foreground flex flex-wrap items-center gap-x-3">
				{same ? (
					t("runs.agents.handoffs.unchanged")
				) : (
					<>
						<span className="text-chart-error-ink">- {t("runs.agents.handoffs.sent")}</span>
						<span className="text-chart-success-ink">+ {t("runs.agents.handoffs.delivered")}</span>
					</>
				)}
			</span>
			<PayloadBlock lines={lines} />
		</div>
	);
}

function splitPlain(text: string): DiffLine[] {
	return text
		.replace(/\n$/, "")
		.split("\n")
		.map((line) => ({ op: "same", text: line }));
}

function PayloadBlock({ lines }: { lines: readonly DiffLine[] }) {
	const showMarks = lines.some((line) => line.op !== "same");
	return (
		<pre className="bench-mono-block max-h-[22rem] overflow-auto py-2" tabIndex={0}>
			<code>
				{lines.map((line, i) => (
					// Lines repeat, so the position is the only stable key.
					<span key={`line-${i}`} className={cn("block px-3 whitespace-pre-wrap break-words", DIFF_CLASS[line.op])} data-op={line.op}>
						{showMarks && <span className="mr-2 opacity-70 select-none">{DIFF_MARK[line.op]}</span>}
						{line.text.length > 0 ? line.text : " "}
					</span>
				))}
			</code>
		</pre>
	);
}