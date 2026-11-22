import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { useGetAgentVersionsQuery, type EvaluationMode } from "@/lib/store/apis/evaluationsApi";
import "@/lib/types/evaluateI18n";
import { cn } from "@/lib/utils";
import { BarChart3, Check, Cpu, GitCompare, Swords } from "lucide-react";
import type { ComponentType } from "react";
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
	const opponents = opponentChoices((agents.data?.agents ?? []) as AgentWithCaps[], draft.agent).filter((a) => a.id !== BARE_MODEL_AGENT);

	return (
		<div className="flex flex-col gap-6" data-testid="evaluate-step-2">
			<div className="flex flex-col gap-1">
				<h2 className="text-lg font-semibold">{t("learn.heading")}</h2>
				<p className="text-muted-foreground text-sm">{t("learn.hint")}</p>
			</div>

			<div className="grid gap-3 sm:grid-cols-2" role="radiogroup">
				{LEARN_MODES.map((m) => {
					const Icon = MODE_ICON[m];
					const on = draft.mode === m;
					return (
						<button
							key={m}
							type="button"
							role="radio"
							aria-checked={on}
							onClick={() => update({ mode: m })}
							data-testid={`evaluate-mode-${m}`}
							className={cn(
								"bg-card relative flex items-start gap-3 rounded-xl border p-4 text-left transition-[border-color,box-shadow] duration-150",
								"hover:border-border focus-visible:ring-ring/50 outline-none focus-visible:ring-2",
								on ? "border-primary ring-primary/30 ring-2" : "border-border/70",
							)}
						>
							{on && <Check className="text-primary absolute top-3 right-3 size-4" aria-hidden />}
							<span className="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-lg">
								<Icon className="size-4" />
							</span>
							<span className="flex flex-col gap-1 pr-6">
								<span className="text-sm font-semibold">{t(`learn.mode.${m}`)}</span>
								<span className="text-muted-foreground text-sm">{t(`learn.modeHelp.${m}`)}</span>
							</span>
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
					<h3 className="text-sm font-semibold">{t("learn.arms")}</h3>
					<ul className="flex flex-wrap gap-2">
						{draft.arms.map((a) => (
							<li key={a.label} className="bg-muted rounded-lg px-3 py-1.5 font-mono text-xs">
								{t("learn.armLabel", { label: a.label })}: {a.agent || "?"}
								{a.version != null
									? ` v${a.version}`
									: latest !== undefined && a.agent === draft.agent
										? tg("learn.latest", { n: latest })
										: ""}
								{a.model ? ` / ${a.model}` : ""}
							</li>
						))}
					</ul>
				</div>
			)}
		</div>
	);
}

export default Step2Learn;