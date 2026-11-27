import { NativeSelect } from "@/components/ui/select";
import { isBaselineAgent, isTestAgent } from "@/app/agents/views/agentDisplay";
import type { AgentSummary } from "@/lib/types/bench";
import "@/lib/types/agentFirstI18n";
import { useTranslation } from "react-i18next";

/**
 * Agents a picker offers: the org's own first, then baselines; broken files never, test doubles
 * only when keepTests is set (a caller that hides them behind its own "show test items" chip).
 */
export function pickableAgents(agents: readonly AgentSummary[], keepTests = false): { yours: AgentSummary[]; baselines: AgentSummary[] } {
	const ok = agents.filter((a) => a.id && !a.error && (keepTests || !isTestAgent(a)));
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
		<label className="flex w-full flex-col gap-1.5 text-xs font-medium sm:w-auto">
			{label ?? t("picker.agent")}
			<NativeSelect
				className="min-w-56"
				wrapperClassName="w-full sm:w-auto"
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
			</NativeSelect>
		</label>
	);
}
