import { JobRunButton } from "@/components/jobs/jobRunButton";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import type { EvaluationSpec } from "@/lib/store/apis/evaluationsApi";
import { useTranslation } from "react-i18next";
import { type AgentWithCaps, isBuiltinAgent } from "./evaluate.utils";
import "./ufd3I18n";

/**
 * Decision 2 (M3-38, X-113): before paying for an evaluation, a new user may run the free agent
 * check (job agent.check): their own agent, one task, the scripted practice model, $0 of model
 * money, on a worker. It is not a practice evaluation and gives no score.
 */

export type FreeCheckPlan =
	| { ok: true; params: { agent: string; suite: string }; suite: string }
	| { ok: false; reason: "noAgent" | "builtin" | "ownsModel" | "noBenchmark"; agent?: string };

/** What the free check would run for this spec, or why it cannot run. `agents` is GET /agents. */
export function freeCheckPlan(spec: Pick<EvaluationSpec, "arms" | "benchmarks">, agents: readonly AgentWithCaps[]): FreeCheckPlan {
	const id = spec.arms[0]?.agent ?? "";
	const agent = id ? agents.find((a) => a.id === id) : undefined;
	if (!agent) return { ok: false, reason: "noAgent" };
	if (isBuiltinAgent(agent)) return { ok: false, reason: "builtin", agent: agent.id };
	// Endpoint agents make their own model calls, which the practice model cannot answer.
	if (agent.owns_model) return { ok: false, reason: "ownsModel", agent: agent.id };
	const suite = spec.benchmarks[0] ?? "";
	if (!suite) return { ok: false, reason: "noBenchmark" };
	return { ok: true, params: { agent: agent.path || agent.id, suite }, suite };
}

export function FreeCheck({ spec }: { spec: Pick<EvaluationSpec, "arms" | "benchmarks"> }) {
	const { t } = useTranslation("ufd3");
	const q = useGetAgentsQuery();
	const agents = (q.data?.agents ?? []) as AgentWithCaps[];
	if (q.isLoading) return null;
	const plan = freeCheckPlan(spec, agents);
	return (
		<section className="bg-muted/40 flex flex-col gap-2 rounded-lg border p-3" data-testid="step5-free-check">
			<h3 className="text-sm font-semibold">{t("freeCheck.title")}</h3>
			{plan.ok ? (
				<>
					<p className="text-muted-foreground text-xs" data-testid="step5-free-check-what">
						{t("freeCheck.what", { suite: plan.suite })}
					</p>
					<JobRunButton kind="agent.check" params={plan.params} label={t("freeCheck.button")} variant="outline" />
				</>
			) : (
				<p className="text-muted-foreground text-xs" data-testid="step5-free-check-reason">
					{t(`freeCheck.${plan.reason}` as never, { agent: plan.agent ?? "" })}
				</p>
			)}
		</section>
	);
}
