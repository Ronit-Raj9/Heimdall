import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import type { AgentSummary } from "@/lib/types/bench";
import { useParams } from "@tanstack/react-router";
import type { ReactNode } from "react";

/** Props both agent ops tabs take; agentId falls back to the route's $agentId. */
export interface AgentOpsTabProps {
	agentId?: string;
}

/** The agent id of an agent ops tab: the prop, else the route parameter. */
export function useAgentId(props: AgentOpsTabProps): string {
	const params = useParams({ strict: false }) as { agentId?: string };
	return props.agentId ?? params.agentId ?? "";
}

/** The agent's summary from GET /agents (undefined while loading or when missing). */
export function useAgentSummary(id: string): { agent?: AgentSummary; isLoading: boolean } {
	const { data, isLoading } = useGetAgentsQuery();
	return { agent: data?.agents.find((a) => a.id === id), isLoading };
}

/** A titled block of one tab. */
export function OpsSection({ title, hint, children, testId }: { title: string; hint?: ReactNode; children: ReactNode; testId?: string }) {
	return (
		<section className="border-border space-y-3 rounded-lg border p-4" data-testid={testId}>
			<div>
				{/* An h2: the tab sits right under the page h1 (Q-127). */}
				<h2 className="text-sm font-semibold">{title}</h2>
				{hint && <p className="text-muted-foreground mt-1 text-xs">{hint}</p>}
			</div>
			{children}
		</section>
	);
}

/** A labelled form field. */
export function Field({ label, children }: { label: string; children: ReactNode }) {
	return (
		<label className="flex flex-col gap-1 text-xs">
			<span className="text-muted-foreground">{label}</span>
			{children}
		</label>
	);
}
