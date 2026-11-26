import { RoleGate } from "@/components/platform/accessNotes";
import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useScanAgentSkillMutation } from "@/lib/store/apis/agentopsApi";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

/** The skill the gate refused, from the fixed gate sentence (adapters/skillgate.go). */
export interface SkillRefusal {
	skill: string;
	agent: string;
	/** The skill changed after a passing scan (else it never passed one). */
	changed: boolean;
}

// The gate sentence (adapters/skillgate.go) or its plain form (handlers/planerrors.go), matched anywhere in the text.
const GATE =
	/(?:^|[\s:])(?:skill (\S+) in agents\/([^/\s]+)\/skills (has no passing scan|changed after its scan)|Skill (\S+) of agent (\S+) (has no passing safety scan|changed after its safety scan))/;

/** Parses a benchmark's skip reason; null when the skill gate did not refuse it (fix wave 3, L5). */
export function parseSkillRefusal(text: string | undefined): SkillRefusal | null {
	const m = GATE.exec(text ?? "");
	return m ? { skill: m[1] ?? m[4], agent: m[2] ?? m[5], changed: (m[3] ?? m[6]).startsWith("changed") } : null;
}

/**
 * The next step for a benchmark the skill gate refused (fix wave 3, L5): why, "Scan it now" (members) with the
 * scan's result in place, and a link to the agent's Tools tab.
 */
export function SkillScanProblem({ refusal, testId }: { refusal: SkillRefusal; testId: string }) {
	const { t } = useTranslation("evaluations");
	const [scan, state] = useScanAgentSkillMutation();
	const record = state.data?.record;
	return (
		<div className="flex flex-col gap-2 text-sm" data-testid={testId}>
			<p>
				{refusal.changed
					? t("skill.changed", { skill: refusal.skill })
					: t("skill.notScanned", { skill: refusal.skill, agent: refusal.agent })}
			</p>
			<div className="flex flex-wrap items-center gap-3">
				<RoleGate min="member" fallback={null}>
					{!record && (
						<Button
							size="sm"
							variant="outline"
							isLoading={state.isLoading}
							onClick={() => void scan({ id: refusal.agent, name: refusal.skill })}
							dataTestId={`${testId}-scan`}
						>
							{t("skill.scan")}
						</Button>
					)}
				</RoleGate>
				<Link
					to="/agents/$agentId"
					params={{ agentId: refusal.agent }}
					search={{ tab: "tools" } as never}
					className="text-primary text-sm hover:underline"
					data-testid={`${testId}-tools`}
				>
					{t("skill.tools")}
				</Link>
			</div>
			{record && record.decision !== "blocked" && (
				<p className="text-chart-success-ink" role="status" data-testid={`${testId}-admitted`}>
					{t("skill.admitted")}
				</p>
			)}
			{record && record.decision === "blocked" && (
				<div className="flex flex-col gap-1" role="status" data-testid={`${testId}-blocked`}>
					<p className="text-chart-error-ink font-medium">{t("skill.blocked")}</p>
					<ul className="text-muted-foreground list-disc pl-5 text-xs">
						{(record.findings ?? []).map((f, i) => (
							<li key={`${f.rule}-${i}`}>
								{f.file}
								{f.line ? `:${f.line}` : ""}: {f.detail}
							</li>
						))}
					</ul>
					<Link
						to="/agents/$agentId"
						params={{ agentId: refusal.agent }}
						search={{ tab: "tools" } as never}
						className="text-primary w-fit text-xs hover:underline"
					>
						{t("skill.remove")}
					</Link>
				</div>
			)}
			{state.error ? (
				<p className="text-chart-error-ink text-xs" role="alert">
					{t("skill.error", { error: getBenchErrorMessage(state.error) })}
				</p>
			) : null}
		</div>
	);
}