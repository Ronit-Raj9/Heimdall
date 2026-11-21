import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { BASELINE_AGENTS, useGetAgentVersionsQuery, type Capability } from "@/lib/store/apis/evaluationsApi";
import "@/lib/types/evaluateI18n";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { Bot, Check, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";
import { armsFor, useEvaluateDraft } from "./evaluateDraft";
import { agentCapabilities, REGISTER_RETURN, splitAgents, type AgentWithCaps } from "./evaluate.utils";

export function CapabilityChips({ caps }: { caps: Capability[] }) {
	const { t } = useTranslation("evaluate");
	return (
		<span className="flex flex-wrap gap-1">
			{caps.map((c) => (
				<Badge key={c} variant="neutral" data-testid={`cap-${c}`}>
					{t(`capability.${c}`, { defaultValue: c })}
				</Badge>
			))}
		</span>
	);
}

function AgentCard({ agent, selected, onPick }: { agent: AgentWithCaps; selected: boolean; onPick(): void }) {
	const { t } = useTranslation("evaluate");
	return (
		<button
			type="button"
			onClick={onPick}
			aria-pressed={selected}
			data-testid={`evaluate-agent-${agent.id}`}
			className={cn(
				"bg-card relative flex min-h-[120px] flex-col gap-3 rounded-xl border p-4 text-left transition-[border-color,box-shadow] duration-150",
				"hover:border-border focus-visible:ring-ring/50 outline-none focus-visible:ring-2",
				selected ? "border-primary ring-primary/30 ring-2" : "border-border/70",
			)}
		>
			{selected && <Check className="text-primary absolute top-3 right-3 size-4" aria-hidden />}
			<span className="flex items-center gap-3 pr-6">
				<span className="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-lg">
					<Bot className="size-4" aria-hidden />
				</span>
				<span className="flex min-w-0 flex-col">
					<span className="truncate text-sm font-semibold">{agent.id}</span>
					{agent.model && (
						<span className="text-muted-foreground truncate font-mono text-[11px]" title={t("agent.defaultModel")}>
							{agent.model}
						</span>
					)}
				</span>
				{BASELINE_AGENTS.includes(agent.id) && (
					<Badge variant="info" className="ml-auto">
						{t("agent.baseline")}
					</Badge>
				)}
			</span>
			<span className="mt-auto">
				<CapabilityChips caps={agentCapabilities(agent)} />
			</span>
		</button>
	);
}

/** Version picker for the chosen agent, from GET /agents/{id}/versions. */
export function VersionPicker({
	agent,
	value,
	onChange,
	label,
	testId,
}: {
	agent: string;
	value: number | null;
	onChange(v: number | null): void;
	label?: string;
	testId?: string;
}) {
	const { t } = useTranslation("evaluate");
	const q = useGetAgentVersionsQuery(agent);
	const versions = [...(q.data?.versions ?? [])].sort((a, b) => b.version - a.version);
	return (
		<label className="flex flex-col gap-1.5 text-sm">
			<span className="font-medium">{label ?? t("agent.version")}</span>
			<select
				className="border-input bg-background h-9 w-56 rounded-lg border px-2 text-sm"
				value={value == null ? "" : String(value)}
				onChange={(e) => onChange(e.target.value === "" ? null : Number(e.target.value))}
				data-testid={testId ?? "evaluate-version"}
				disabled={q.isLoading}
			>
				<option value="">{t("agent.latest")}</option>
				{versions.map((v) => (
					<option key={v.version} value={v.version}>
						{v.runs != null ? t("agent.versionRuns", { n: v.version, runs: v.runs }) : t("agent.versionOption", { n: v.version })}
					</option>
				))}
			</select>
			{!q.isLoading && versions.length === 0 && <span className="text-muted-foreground text-xs">{t("agent.noVersions")}</span>}
		</label>
	);
}

/** Step 1: pick the agent under test (org or built-in), or register one, and its version. */
export function Step1Agent() {
	const { t } = useTranslation("evaluate");
	const { draft, update } = useEvaluateDraft();
	const q = useGetAgentsQuery();
	const all = (q.data?.agents ?? []) as AgentWithCaps[];
	const { own, builtin } = splitAgents(all);

	const pick = (a: AgentWithCaps) => {
		if (a.id === draft.agent) return;
		const next = { ...draft, agent: a.id, version: null, compareVersion: null, arms: [] };
		update({ agent: a.id, version: null, compareVersion: null, opponent: draft.opponent === a.id ? null : draft.opponent, arms: armsFor(next, a.model) });
	};

	const grid = (list: AgentWithCaps[]) => (
		<div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
			{list.map((a) => (
				<AgentCard key={a.id} agent={a} selected={draft.agent === a.id} onPick={() => pick(a)} />
			))}
		</div>
	);

	return (
		<div className="flex flex-col gap-6" data-testid="evaluate-step-1">
			<div className="flex flex-col gap-1">
				<h2 className="text-lg font-semibold">{t("agent.heading")}</h2>
				<p className="text-muted-foreground text-sm">{t("agent.hint")}</p>
			</div>

			{q.isLoading ? (
				<p className="text-muted-foreground text-sm" data-testid="evaluate-agents-loading">
					{t("agent.loading")}
				</p>
			) : q.isError ? (
				<div className="flex items-center gap-3 text-sm">
					<span className="text-destructive">{t("agent.loadError")}</span>
					<Button variant="outline" size="sm" onClick={() => void q.refetch()}>
						{t("agent.retry")}
					</Button>
				</div>
			) : (
				<>
					<section className="flex flex-col gap-3">
						<div className="flex flex-wrap items-center justify-between gap-2">
							<h3 className="text-sm font-semibold">{t("agent.yours")}</h3>
							<Button asChild variant="outline" size="sm">
								<Link to="/agents/new" search={{ return: REGISTER_RETURN } as never} data-testid="evaluate-register">
									<Plus />
									{t("agent.register")}
								</Link>
							</Button>
						</div>
						{own.length ? grid(own) : <p className="text-muted-foreground text-sm">{t("agent.noneYours")} {t("agent.registerHint")}</p>}
					</section>
					<section className="flex flex-col gap-3">
						<h3 className="text-sm font-semibold">{t("agent.builtin")}</h3>
						{builtin.length ? grid(builtin) : <p className="text-muted-foreground text-sm">{t("agent.empty")}</p>}
					</section>
				</>
			)}

			{draft.agent ? (
				<VersionPicker agent={draft.agent} value={draft.version} onChange={(v) => update({ version: v })} />
			) : (
				<p className="text-muted-foreground text-sm">{t("agent.pickFirst")}</p>
			)}
		</div>
	);
}

export default Step1Agent;
