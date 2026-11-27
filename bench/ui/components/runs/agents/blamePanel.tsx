import { clipText } from "@/components/runs/stepTimeline.utils";
import { ToneBadge } from "@/components/suites/suiteBadges";
import type { Tone } from "@/components/suites/suites.utils";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getLocale } from "@/lib/i18n";
import type { Blame, BlameCandidate, BlameReplay } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ArrowRight, Ban, CircleHelp, Crosshair, FlaskConical, ShieldCheck, Users, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import { AgentId, RoleChip, StrengthBadge, UnknownValue, sourceLabel } from "./agentBadges";
import {
	EFFECT_TONE,
	REPLAY_TONE,
	TIER_TONE,
	formatUsd4,
	isBlamedCandidate,
	knownEffect,
	knownReplayStatus,
	knownTier,
	tierNamesAgent,
	timeOfDay,
} from "./agents.utils";

const TIER_ICON: Record<string, LucideIcon> = {
	replay_validated: FlaskConical,
	gate_proven: ShieldCheck,
	unattributed: CircleHelp,
	single_agent: Users,
};

const TONE_STRIPE: Record<Tone, string> = {
	good: "before:bg-chart-success",
	warn: "before:bg-chart-warning",
	bad: "before:bg-chart-error",
	neutral: "before:bg-border",
};

/** The evidence ladder, strongest first. The LLM judge rung is drawn after it, struck out: not offered. */
const LADDER = ["replay_validated", "gate_proven", "unattributed"] as const;

/**
 * Which agent the run's failure is blamed on and how strongly that is known: a counterfactual
 * replay that flipped the outcome (replay_validated), the first agent whose recorded step tripped a
 * fired gate (gate_proven), or no agent provable (unattributed). Lists the gate, step, time and
 * how the step's agent is known, the evidence, every candidate, the references no agent could be
 * found for, the replay and notes. Says outright that an LLM-judge tier is not offered. Ids,
 * roles, refs, detail and notes are agent-influenced text: clipped React text nodes.
 */
export function BlamePanel({ blame, arm }: { blame: Blame; arm: string }) {
	const { t } = useTranslation("bench");
	const tier = knownTier(blame.tier);
	const tone: Tone = tier ? TIER_TONE[tier] : "neutral";
	const testId = `agents-blame-${arm}`;
	const named = tierNamesAgent(tier);
	const time = timeOfDay(blame.at);

	return (
		<section
			className={cn(
				"bg-card relative flex min-w-0 flex-col gap-4 overflow-hidden rounded-lg border p-4 pl-5 shadow-(--shadow-card) before:absolute before:inset-y-0 before:left-0 before:w-1",
				TONE_STRIPE[tone],
			)}
			aria-label={t("runs.agents.blame.title")}
			data-testid={testId}
			data-tier={blame.tier}
		>
			<header className="flex flex-wrap items-center gap-2">
				<h4 className="text-foreground text-sm font-semibold tracking-tight">{t("runs.agents.blame.title")}</h4>
				<ToneBadge
					tone={tone}
					icon={(tier && TIER_ICON[tier]) || undefined}
					hint={tier ? t(`runs.agents.blame.tierHint.${tier}`) : undefined}
					testId={`${testId}-tier`}
				>
					{tier ? t(`runs.agents.blame.tier.${tier}`) : clipText(blame.tier, 32)}
				</ToneBadge>
				<span className="text-muted-foreground text-xs">
					{t("runs.agents.blame.outcome")} <span className="text-foreground font-mono">{clipText(blame.outcome, 32)}</span>
				</span>
				{blame.failed && (
					<ToneBadge tone="bad" testId={`${testId}-failed`}>
						{t("runs.agents.blame.failed")}
					</ToneBadge>
				)}
				<span className="text-muted-foreground ml-auto font-mono text-[11px]">
					{t("runs.agents.blame.version", { version: clipText(blame.version, 16) })}
				</span>
			</header>

			<EvidenceLadder current={tier} testId={`${testId}-ladder`} />

			{named ? (
				<div className="flex min-w-0 flex-col gap-3">
					<div className="flex flex-wrap items-center gap-2">
						<Crosshair className="text-chart-error-ink size-4 shrink-0" aria-hidden="true" />
						<span className="text-muted-foreground text-xs">{t("runs.agents.blame.blamed")}</span>
						<AgentId id={blame.agent_id} className="text-base font-semibold" testId={`${testId}-agent`} />
						<RoleChip role={blame.role} testId={`${testId}-role`} />
					</div>
					<dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-xs sm:grid-cols-[auto_1fr_auto_1fr]">
						<Fact label={t("runs.agents.blame.gate")}>
							{blame.gate ? (
								<span className="font-mono">{clipText(blame.gate, 64)}</span>
							) : (
								<UnknownValue hint={t("runs.agents.notRecorded")} />
							)}
						</Fact>
						<Fact label={t("runs.agents.blame.step")}>
							{blame.step_seq ? (
								<span className="font-mono tabular-nums">#{blame.step_seq}</span>
							) : (
								<UnknownValue hint={t("runs.agents.notRecorded")} />
							)}
						</Fact>
						<Fact label={t("runs.agents.blame.time")}>
							{time ? <span className="font-mono tabular-nums">{time}</span> : <UnknownValue hint={t("runs.agents.notRecorded")} />}
						</Fact>
						<Fact label={t("runs.agents.blame.knownBy")}>
							<span className="inline-flex flex-wrap items-center gap-1.5">
								<StrengthBadge strength={blame.strength} source={blame.agent_source} testId={`${testId}-strength`} />
								{blame.agent_source && (
									<span className="text-muted-foreground font-mono text-[11px]">{sourceLabel(t, blame.agent_source)}</span>
								)}
							</span>
						</Fact>
					</dl>
				</div>
			) : null}

			{blame.detail && (
				<p className="text-foreground max-w-prose text-sm leading-relaxed break-words" data-testid={`${testId}-detail`}>
					{clipText(blame.detail, 600)}
				</p>
			)}

			{blame.evidence && blame.evidence.length > 0 && (
				<RefList title={t("runs.agents.blame.evidence")} refs={blame.evidence} testId={`${testId}-evidence`} />
			)}

			{blame.candidates && blame.candidates.length > 0 && (
				<Candidates candidates={blame.candidates} blame={blame} testId={`${testId}-candidates`} />
			)}

			{blame.unplaced && blame.unplaced.length > 0 && (
				<RefList
					title={t("runs.agents.blame.unplaced")}
					hint={t("runs.agents.blame.unplacedHint")}
					refs={blame.unplaced}
					testId={`${testId}-unplaced`}
					warn
				/>
			)}

			{blame.replay && <ReplayBlock replay={blame.replay} testId={`${testId}-replay`} />}

			{blame.notes && blame.notes.length > 0 && (
				<div className="flex flex-col gap-1" data-testid={`${testId}-notes`}>
					<h5 className="text-muted-foreground text-xs font-medium">{t("runs.agents.blame.notes")}</h5>
					<ul className="text-muted-foreground list-disc pl-5 text-xs leading-relaxed">
						{blame.notes.map((note, i) => (
							// Notes can repeat, so the position is part of the key.
							<li key={`${i}-${note}`} className="break-words">
								{clipText(note, 400)}
							</li>
						))}
					</ul>
				</div>
			)}
		</section>
	);
}

/** The tiers as rungs, the current one filled; the LLM judge rung struck out with why it is not offered. */
function EvidenceLadder({ current, testId }: { current: string | undefined; testId: string }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex min-w-0 flex-col gap-1.5" data-testid={testId}>
			<ol className="flex min-w-0 flex-wrap items-stretch gap-1" aria-label={t("runs.agents.blame.ladder")}>
				{LADDER.map((rung) => {
					const on = rung === current;
					const Icon = TIER_ICON[rung];
					return (
						<li
							key={rung}
							aria-current={on ? "step" : undefined}
							data-on={on}
							className={cn(
								"inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-[11px] font-medium",
								on ? cn("border-current", toneText(TIER_TONE[rung]), "bg-muted/60") : "text-muted-foreground border-dashed opacity-80",
							)}
						>
							<Icon className="size-3 shrink-0" aria-hidden="true" />
							{t(`runs.agents.blame.tier.${rung}`)}
						</li>
					);
				})}
				<li
					className="text-muted-foreground inline-flex items-center gap-1.5 rounded-md border border-dashed px-2 py-1 text-[11px] line-through decoration-1 opacity-70"
					data-testid={`${testId}-judge`}
				>
					<Ban className="size-3 shrink-0" aria-hidden="true" />
					{t("runs.agents.blame.judgeRung")}
				</li>
			</ol>
			<p className="text-muted-foreground text-[11px]" data-testid={`${testId}-judge-note`}>
				{t("runs.agents.blame.judgeNotOffered")}
			</p>
			{current === "single_agent" && <p className="text-muted-foreground text-[11px]">{t("runs.agents.blame.tierHint.single_agent")}</p>}
		</div>
	);
}

function toneText(tone: Tone): string {
	return { good: "text-chart-success-ink", warn: "text-chart-warning-ink", bad: "text-chart-error-ink", neutral: "text-foreground" }[tone];
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<>
			<dt className="text-muted-foreground self-center">{label}</dt>
			<dd className="text-foreground min-w-0 self-center">{children}</dd>
		</>
	);
}

/** References as inert monospace chips: never links, never HTML. */
function RefList({
	title,
	hint,
	refs,
	testId,
	warn = false,
}: {
	title: string;
	hint?: string;
	refs: readonly string[];
	testId: string;
	warn?: boolean;
}) {
	return (
		<div className="flex min-w-0 flex-col gap-1" data-testid={testId}>
			<h5 className="text-muted-foreground text-xs font-medium" title={hint}>
				{title}
			</h5>
			{hint && <p className="text-muted-foreground text-[11px]">{hint}</p>}
			<ul className="flex flex-wrap gap-1.5">
				{refs.map((ref, i) => (
					// References can repeat, so the position is part of the key.
					<li
						key={`${i}-${ref}`}
						className={cn("bg-muted/60 rounded-md border px-1.5 py-0.5 font-mono text-[11px] break-all", warn && "border-chart-warning/50")}
					>
						{clipText(ref, 120)}
					</li>
				))}
			</ul>
		</div>
	);
}

function Candidates({ candidates, blame, testId }: { candidates: readonly BlameCandidate[]; blame: Blame; testId: string }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex min-w-0 flex-col gap-1.5">
			<h5 className="text-muted-foreground text-xs font-medium">{t("runs.agents.blame.candidates")}</h5>
			<Table data-testid={testId} containerClassName="rounded-md border">
				<TableHeader>
					<TableRow>
						<TableHead>{t("runs.agents.table.agent")}</TableHead>
						<TableHead>{t("runs.agents.table.role")}</TableHead>
						<TableHead>{t("runs.agents.blame.gate")}</TableHead>
						<TableHead>{t("runs.agents.blame.effect")}</TableHead>
						<TableHead>{t("runs.agents.blame.ref")}</TableHead>
						<TableHead className="text-right">{t("runs.agents.blame.step")}</TableHead>
						<TableHead>{t("runs.agents.blame.time")}</TableHead>
						<TableHead>{t("runs.agents.blame.source")}</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{candidates.map((c, i) => {
						const picked = isBlamedCandidate(c, blame);
						const effect = knownEffect(c.effect);
						return (
							<TableRow
								// Candidates can repeat a ref, so the position is part of the key.
								key={`${i}-${c.agent_id}-${c.ref}`}
								className={cn(picked && "bg-chart-error/5")}
								data-testid={`${testId}-${i}`}
								data-blamed={picked}
							>
								<TableCell className="text-xs">
									<span className="inline-flex items-center gap-1.5">
										<AgentId id={c.agent_id} />
										{picked && (
											<ToneBadge tone="bad" icon={Crosshair} testId={`${testId}-${i}-blamed`}>
												{t("runs.agents.blame.picked")}
											</ToneBadge>
										)}
									</span>
								</TableCell>
								<TableCell>
									<RoleChip role={c.role} />
								</TableCell>
								<TableCell className="font-mono text-xs">{clipText(c.gate, 64)}</TableCell>
								<TableCell>
									<ToneBadge tone={effect ? EFFECT_TONE[effect] : "neutral"}>
										{effect ? t(`runs.agents.blame.effectName.${effect}`) : clipText(c.effect, 24)}
									</ToneBadge>
								</TableCell>
								<TableCell className="max-w-[16rem] font-mono text-[11px] break-all whitespace-normal">{clipText(c.ref, 80)}</TableCell>
								<TableCell className="text-right font-mono text-xs">{c.step_seq ? `#${c.step_seq}` : ""}</TableCell>
								<TableCell className="text-muted-foreground font-mono text-xs">{timeOfDay(c.at) ?? ""}</TableCell>
								<TableCell className="text-muted-foreground font-mono text-[11px]">
									{c.agent_source ? sourceLabel(t, c.agent_source) : ""}
								</TableCell>
							</TableRow>
						);
					})}
				</TableBody>
			</Table>
		</div>
	);
}

/** The tier 2 counterfactual: status, who was replaced by what, the outcome before and after, gates that still fired, and its cost. */
function ReplayBlock({ replay, testId }: { replay: BlameReplay; testId: string }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const status = knownReplayStatus(replay.status);
	return (
		<div className="bg-muted/30 flex min-w-0 flex-col gap-2 rounded-md border p-3" data-testid={testId} data-status={replay.status}>
			<div className="flex flex-wrap items-center gap-2">
				<FlaskConical className="text-muted-foreground size-3.5 shrink-0" aria-hidden="true" />
				<h5 className="text-foreground text-xs font-semibold">{t("runs.agents.replay.title")}</h5>
				<ToneBadge
					tone={status ? REPLAY_TONE[status] : "neutral"}
					hint={status ? t(`runs.agents.replay.statusHint.${status}`) : undefined}
					testId={`${testId}-status`}
				>
					{status ? t(`runs.agents.replay.status.${status}`) : clipText(replay.status, 24)}
				</ToneBadge>
			</div>
			<dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-xs">
				<Fact label={t("runs.agents.replay.suspect")}>
					<AgentId id={replay.suspect} testId={`${testId}-suspect`} />
				</Fact>
				<Fact label={t("runs.agents.replay.stub")}>
					<span className="font-mono break-all">{clipText(replay.stub, 80)}</span>
				</Fact>
				<Fact label={t("runs.agents.replay.outcome")}>
					<span className="inline-flex items-center gap-1.5 font-mono" data-testid={`${testId}-outcome`}>
						{clipText(replay.outcome_before, 24)}
						<ArrowRight className="text-muted-foreground size-3" aria-hidden="true" />
						<span className="sr-only">{t("runs.agents.handoffs.to")}</span>
						{replay.outcome_after ? (
							clipText(replay.outcome_after, 24)
						) : (
							<span className="text-muted-foreground font-sans italic">{t("runs.agents.replay.noOutcome")}</span>
						)}
					</span>
				</Fact>
				<Fact label={t("runs.agents.replay.gatesAfter")}>
					{replay.gates_after && replay.gates_after.length > 0 ? (
						<span className="flex flex-wrap gap-1">
							{replay.gates_after.map((gate, i) => (
								<span key={`${i}-${gate}`} className="bg-muted rounded-sm border px-1 font-mono text-[11px]">
									{clipText(gate, 64)}
								</span>
							))}
						</span>
					) : (
						<span className="text-muted-foreground">{t("runs.agents.replay.noGates")}</span>
					)}
				</Fact>
				<Fact label={t("runs.agents.replay.cost")}>
					{replay.cost_usd !== undefined && replay.cost_usd !== null && Number.isFinite(replay.cost_usd) ? (
						<span className="tabular-nums" data-testid={`${testId}-cost`}>
							{formatUsd4(replay.cost_usd, locale)}
						</span>
					) : (
						<UnknownValue hint={t("runs.agents.notRecorded")} testId={`${testId}-cost-unknown`} />
					)}
				</Fact>
				{replay.run_id && (
					<Fact label={t("runs.agents.replay.run")}>
						<span className="font-mono break-all">
							{clipText(replay.run_id, 64)}
							{replay.exp_id && <span className="text-muted-foreground"> ({clipText(replay.exp_id, 64)})</span>}
						</span>
					</Fact>
				)}
			</dl>
			{replay.detail && <p className="text-muted-foreground text-xs break-words">{clipText(replay.detail, 400)}</p>}
		</div>
	);
}