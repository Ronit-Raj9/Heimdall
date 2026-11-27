import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { useGetAgentVersionsQuery, type EvaluationMode } from "@/lib/store/apis/evaluationsApi";
import "@/lib/types/evaluateI18n";
import { cn } from "@/lib/utils";
import { BarChart3, Check, Cpu, GitCompare, ShieldAlert, Swords } from "lucide-react";
import type { ComponentType, KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "@tanstack/react-router";
import { BARE_MODEL_AGENT, useEvaluateDraft } from "./evaluateDraft";
import { latestVersion, LEARN_MODES, opponentChoices, type AgentWithCaps } from "./evaluate.utils";
import "./gw09I18n";
import { VersionPicker } from "./step1Agent";

const MODE_ICON: Record<EvaluationMode, ComponentType<{ className?: string }>> = {
	score: BarChart3,
	versions: GitCompare,
	models: Cpu,
	opponent: Swords,
};

/** A step 2 goal: one of the modes, or the red-team goal, a score evaluation with the red-team writer first (M3-48). */
export type LearnGoal = EvaluationMode | "redteam";

export const LEARN_GOALS: LearnGoal[] = [...LEARN_MODES, "redteam"];

/** The goal the draft is on. */
export function goalOf(d: { mode: EvaluationMode; redTeam?: boolean }): LearnGoal {
	return d.mode === "score" && d.redTeam ? "redteam" : d.mode;
}

/** The draft patch for a goal. */
export function goalPatch(g: LearnGoal): { mode: EvaluationMode; redTeam: boolean } {
	return g === "redteam" ? { mode: "score", redTeam: true } : { mode: g, redTeam: false };
}

/** Arrow keys move between the goal rows of the radio group; the focused row is picked, as native radios do (M3-42). */
function goalKeys(e: KeyboardEvent<HTMLElement>): void {
	if (!["ArrowDown", "ArrowUp", "ArrowRight", "ArrowLeft"].includes(e.key)) return;
	const rows = Array.from(e.currentTarget.querySelectorAll<HTMLButtonElement>("[data-goal-row]:not([disabled])"));
	const i = rows.indexOf(document.activeElement as HTMLButtonElement);
	if (rows.length === 0) return;
	e.preventDefault();
	const next = rows[(i + (e.key === "ArrowDown" || e.key === "ArrowRight" ? 1 : rows.length - 1)) % rows.length];
	next?.focus();
	next?.click();
}

/** Step 2: what the user wants to learn; sets spec.mode and the arms. */
export function Step2Learn() {
	const { t } = useTranslation("evaluate");
	const { t: tg } = useTranslation("gw09");
	const { draft, update } = useEvaluateDraft();
	const agents = useGetAgentsQuery();
	const versions = useGetAgentVersionsQuery(draft.agent ?? "", { skip: !draft.agent });
	const versionCount = versions.data?.versions?.length ?? 0;
	// "Latest" (version null) runs the highest saved version: Setup A's effective version (N95).
	const latest = latestVersion(versions.data?.versions);
	const versionA = draft.version ?? latest ?? null;
	const goal = goalOf(draft);
	// With nothing picked yet the first row takes the tab stop.
	const opponents = opponentChoices((agents.data?.agents ?? []) as AgentWithCaps[], draft.agent).filter((a) => a.id !== BARE_MODEL_AGENT);

	return (
		<div className="flex flex-col gap-6" data-testid="evaluate-step-2">
			<div className="flex flex-col gap-1">
				<h2 className="text-lg font-semibold">{t("learn.heading")}</h2>
				<p className="text-muted-foreground text-sm">{t("learn.hint")}</p>
			</div>

			<div className="flex flex-col rounded-lg border" role="radiogroup" aria-label={t("learn.heading")} onKeyDown={goalKeys}>
				{LEARN_GOALS.map((m) => {
					const Icon = m === "redteam" ? ShieldAlert : MODE_ICON[m];
					const on = goal === m;
					// One saved version cannot be compared: the row says why before any click (M3-42).
					const blocked = m === "versions" && !versions.isLoading && Boolean(draft.agent) && versionCount < 2;
					return (
						<button
							key={m}
							type="button"
							role="radio"
							aria-checked={on}
							tabIndex={on ? 0 : -1}
							disabled={blocked}
							onClick={() => update(goalPatch(m))}
							data-testid={`evaluate-mode-${m}`}
							data-goal-row
							className={cn(
								"flex min-h-11 items-start gap-3 border-b px-3 py-2.5 text-left last:border-b-0",
								"hover:bg-muted focus-visible:ring-ring outline-none focus-visible:ring-2 focus-visible:ring-inset",
								"disabled:hover:bg-transparent disabled:cursor-not-allowed",
								on && "bg-primary/5",
							)}
						>
							<Icon className="text-muted-foreground mt-0.5 size-4 shrink-0" />
							<span className="flex min-w-0 flex-1 flex-col gap-0.5">
								<span className={cn("text-sm font-medium", blocked && "text-muted-foreground")}>{t(`learn.mode.${m}`)}</span>
								<span className="text-muted-foreground text-sm">{blocked ? t("learn.needTwoVersions") : t(`learn.modeHelp.${m}`)}</span>
							</span>
							{on && <Check className="text-primary mt-0.5 size-4 shrink-0" aria-hidden />}
						</button>
					);
				})}
			</div>

			{draft.agent && draft.mode === "versions" && (
				<div className="flex flex-col gap-2">
					<VersionPicker
						agent={draft.agent}
						value={draft.compareVersion}
						onChange={(v) => update({ compareVersion: v })}
						label={t("learn.compareVersion")}
						testId="evaluate-compare-version"
						disabledVersion={versionA}
						disabledSuffix={tg("learn.setupA")}
					/>
					{!versions.isLoading && versionCount < 2 && <p className="text-chart-warning-ink text-sm">{t("learn.needTwoVersions")}</p>}
					{draft.compareVersion != null && draft.compareVersion === versionA && (
						<p className="text-chart-warning-ink text-sm">{t("learn.sameVersion")}</p>
					)}
				</div>
			)}

			{draft.mode === "opponent" && (
				<label className="flex flex-col gap-1.5 text-sm">
					<span className="font-medium">{t("learn.opponent")}</span>
					<select
						className="border-input bg-background h-9 w-56 rounded-lg border px-2 text-sm"
						value={draft.opponent ?? ""}
						onChange={(e) => update({ opponent: e.target.value || null })}
						data-testid="evaluate-opponent"
					>
						<option value="">{t("learn.pickOpponent")}</option>
						{opponents.map((a) => (
							<option key={a.id} value={a.id}>
								{a.id}
							</option>
						))}
						{draft.agent !== BARE_MODEL_AGENT && <option value={BARE_MODEL_AGENT}>{t("learn.bareModel")}</option>}
					</select>
					{draft.opponent === BARE_MODEL_AGENT && (
						<span className="text-muted-foreground max-w-xl text-xs" data-testid="evaluate-bare-model-help">
							{t("learn.bareModelHelp")}
						</span>
					)}
				</label>
			)}

			{draft.mode === "models" && <p className="text-muted-foreground text-sm">{t("learn.modelsLater")}</p>}
			{draft.mode === "models" && draft.agent && (
				<Link
					to="/agents/$agentId"
					params={{ agentId: draft.agent }}
					search={{ tab: "compare-models" }}
					className="text-primary w-fit text-sm hover:underline"
					data-testid="evaluate-compare-models-link"
				>
					{t("learn.compareModelsLink")}
				</Link>
			)}

			{draft.arms.length > 0 && (
				<div className="flex flex-col gap-2" data-testid="evaluate-arms">
					<h3 className="text-sm font-semibold">{t("learn.whatRuns")}</h3>
					{/* Plain words, not mono ids: "codex v1 on gpt-5.6-terra" (M3-43). */}
					<ul className="flex flex-col gap-1 text-sm">
						{draft.arms.map((a, i) => (
							<li key={a.label}>
								{draft.mode === "models" && i > 0 && !a.model ? (
									t("learn.secondSetupLater")
								) : (
									<>
										{a.agent || "?"}
										{a.version != null
											? ` v${a.version}`
											: latest !== undefined && a.agent === draft.agent
												? tg("learn.latest", { n: latest })
												: ""}
										{a.model ? ` on ${a.model}` : ""}
									</>
								)}
							</li>
						))}
					</ul>
				</div>
			)}
		</div>
	);
}

export default Step2Learn;