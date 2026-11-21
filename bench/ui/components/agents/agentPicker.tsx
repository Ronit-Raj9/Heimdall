import { isBaselineAgent, isTestAgent } from "@/app/agents/views/agentDisplay";
import type { AgentSummary } from "@/lib/types/bench";
import "@/lib/types/agentFirstI18n";
import { useTranslation } from "react-i18next";

/** Agents a picker offers: the org's own first, then baselines; test doubles and broken files never. */
export function pickableAgents(agents: readonly AgentSummary[]): { yours: AgentSummary[]; baselines: AgentSummary[] } {
	const ok = agents.filter((a) => a.id && !a.error && !isTestAgent(a));
	return { yours: ok.filter((a) => !isBaselineAgent(a)), baselines: ok.filter((a) => isBaselineAgent(a)) };
}

/** A labelled agent select: "Your agents" then "Baseline agents". */
export function AgentPicker({
	agents,
	value,
	onChange,
	testId,
	label,
}: {
	agents: readonly AgentSummary[];
	value: string;
	onChange: (id: string) => void;
	testId?: string;
	label?: string;
}) {
	const { t } = useTranslation("agentFirst");
	const { yours, baselines } = pickableAgents(agents);
	return (
		<label className="flex flex-col gap-1 text-xs font-medium">
			{label ?? t("picker.agent")}
			<select
				className="border-input bg-background h-9 min-w-56 rounded-md border px-2 text-sm"
				value={value}
				onChange={(e) => onChange(e.target.value)}
				data-testid={testId}
			>
				<option value="">{t("picker.choose")}</option>
				{yours.length > 0 && (
					<optgroup label={t("list.yours")}>
						{yours.map((a) => (
							<option key={a.id} value={a.id}>
								{a.id}
							</option>
						))}
					</optgroup>
				)}
				{baselines.length > 0 && (
					<optgroup label={t("list.baselines")}>
						{baselines.map((a) => (
							<option key={a.id} value={a.id}>
								{a.id}
							</option>
						))}
					</optgroup>
				)}
			</select>
		</label>
	);
}
