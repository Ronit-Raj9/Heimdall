import { Badge } from "@/components/ui/badge";
import { ShowTestsChip } from "@/components/ui/toolbar";
import { OptionList, type OptionItem } from "@/components/ui/optionList";
import { isTestAgent } from "@/app/agents/views/agentDisplay";
import { isTestObject, withoutTests } from "@/lib/testObjects";
import { Button } from "@/components/ui/button";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import { BASELINE_AGENTS, useGetAgentVersionsQuery, type Capability } from "@/lib/store/apis/evaluationsApi";
import "@/lib/types/evaluateI18n";
import { Link } from "@tanstack/react-router";
import { Check, Plug, Plus } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { agentPickPatch, useEvaluateDraft } from "./evaluateDraft";
import { agentCapabilities, latestVersion, REGISTER_RETURN, splitAgents, type AgentWithCaps } from "./evaluate.utils";
import "./gw09I18n";

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

/** Version picker for the chosen agent, from GET /agents/{id}/versions. */
export function VersionPicker({
	agent,
	value,
	onChange,
	label,
	testId,
	disabledVersion,
	disabledSuffix,
	countHint,
}: {
	agent: string;
	value: number | null;
	onChange(v: number | null): void;
	label?: string;
	testId?: string;
	/** A version that cannot be picked (step 2: Setup A's), shown with `disabledSuffix`. */
	disabledVersion?: number | null;
	disabledSuffix?: string;
	/** Say how many versions there are and which is the latest (step 1). */
	countHint?: boolean;
}) {
	const { t } = useTranslation("evaluate");
	const { t: tg } = useTranslation("gw09");
	const q = useGetAgentVersionsQuery(agent);
	const versions = [...(q.data?.versions ?? [])].sort((a, b) => b.version - a.version);
	const latest = latestVersion(versions);
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
					<option key={v.version} value={v.version} disabled={disabledVersion === v.version}>
						{v.runs != null ? t("agent.versionRuns", { n: v.version, runs: v.runs }) : t("agent.versionOption", { n: v.version })}
						{disabledVersion === v.version ? disabledSuffix : ""}
					</option>
				))}
			</select>
			{!q.isLoading && versions.length === 0 && <span className="text-muted-foreground text-xs">{t("agent.noVersions")}</span>}
			{countHint && versions.length >= 2 && latest !== undefined && (
				<span className="text-muted-foreground text-xs" data-testid="evaluate-versions-hint">
					{tg("agent.versionsHint", { n: versions.length, max: latest })}
				</span>
			)}
		</label>
	);
}

/**
 * Step 1: pick the agent under test (org or built-in), or register one, and its version. `missingAgent` is an agent
 * a link named that this organization does not have (N86); `onPicked` runs on every card click.
 */
export function Step1Agent({ missingAgent, onPicked }: { missingAgent?: string | null; onPicked?(): void } = {}) {
	const { t } = useTranslation("evaluate");
	const { t: tg } = useTranslation("gw09");
	const { draft, update } = useEvaluateDraft();
	const q = useGetAgentsQuery();
	const all = (q.data?.agents ?? []) as AgentWithCaps[];
	// Test agents stay in the split so the chip below can show them and a picked one stays in view.
	const split = splitAgents(all, true);
	// The project's own test agents ("zz-test-...", stub agents) hide behind a chip (M1-18). The wizard keeps its URL
	// for the draft, so the switch is page state here, not ?tests=1.
	const [showTests, setShowTests] = useState(false);
	const ownSplit = withoutTests(
		split.own,
		// The picked agent always stays in view, even a test agent a link or a stored draft chose.
		(a) => a.id !== draft.agent && (isTestAgent(a) || isTestObject(a.id, { description: (a as { description?: string }).description })),
		showTests,
	);
	const own = ownSplit.shown;
	const builtin = split.builtin;

	const pick = (a: AgentWithCaps) => {
		onPicked?.();
		if (a.id === draft.agent) return;
		update(agentPickPatch(draft, a));
	};

	// One shared option list per group (M1-20, Spec 7): a native radio per row, its own search over 8 rows.
	const byId = new Map([...own, ...builtin].map((a) => [a.id, a] as const));
	const options = (list: readonly AgentWithCaps[]): OptionItem[] =>
		list.map((a) => ({
			id: a.id,
			label: a.id,
			meta: a.model || undefined,
			badges: (
				<>
					{BASELINE_AGENTS.includes(a.id) && <Badge variant="info">{t("agent.baseline")}</Badge>}
					<span className="hidden sm:contents">
						<CapabilityChips caps={agentCapabilities(a)} />
					</span>
				</>
			),
		}));
	const grid = (list: AgentWithCaps[], label: string, testId: string) => (
		<OptionList
			mode="single"
			label={label}
			testId={testId}
			items={options(list)}
			value={list.some((a) => a.id === draft.agent) ? (draft.agent ?? "") : ""}
			onChange={(id) => {
				const a = byId.get(id as string);
				if (a) pick(a);
			}}
			searchPlaceholder={t("agent.search")}
		/>
	);

	return (
		<div className="flex flex-col gap-4" data-testid="evaluate-step-1">
			<div className="flex flex-col gap-1">
				<h2 className="text-lg font-semibold">{t("agent.heading")}</h2>
				<p className="text-muted-foreground text-sm">{t("agent.hint")}</p>
			</div>

			{missingAgent && (
				<div
					role="status"
					className="border-chart-warning/40 bg-chart-warning/10 flex flex-wrap items-center gap-3 rounded-lg border px-3 py-2 text-sm"
					data-testid="evaluate-agent-missing"
				>
					<span>{tg("agent.notFound", { id: missingAgent })}</span>
					<Button asChild variant="outline" size="sm">
						<Link to="/agents/new" search={{ return: REGISTER_RETURN } as never} data-testid="evaluate-agent-missing-register">
							<Plus />
							{t("agent.register")}
						</Link>
					</Button>
				</div>
			)}

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
							<span className="flex flex-wrap items-center gap-2">
								<h3 className="text-sm font-semibold">{t("agent.yours")}</h3>
								<ShowTestsChip count={ownSplit.hidden} on={showTests} onChange={setShowTests} testId="evaluate-show-tests" />
							</span>
							{own.length > 0 && (
								<Button asChild variant="outline" size="sm">
									<Link to="/agents/new" search={{ return: REGISTER_RETURN } as never} data-testid="evaluate-register">
										<Plus />
										{t("agent.register")}
									</Link>
								</Button>
							)}
						</div>
						{draft.agent && (
							<div
								className="border-primary/30 bg-primary/5 flex flex-wrap items-end gap-4 rounded-xl border p-3"
								data-testid="evaluate-selected-agent"
							>
								<span className="flex items-center gap-2 pb-2 text-sm font-medium">
									<Check className="text-primary size-4" aria-hidden />
									{tg("agent.selected", { agent: draft.agent })}
								</span>
								<VersionPicker
									agent={draft.agent}
									value={draft.version}
									onChange={(v) => update({ version: v })}
									label={tg("agent.versionOf", { agent: draft.agent })}
									countHint
								/>
							</div>
						)}
						{own.length === 0 ? (
							// The main path for a new user is a full-width row, not a small button (X-26).
							<div className="flex flex-col gap-3 rounded-lg border border-dashed p-4 sm:flex-row sm:items-center" data-testid="evaluate-register-row">
								<span className="flex min-w-0 flex-1 flex-col gap-0.5">
									<span className="text-sm font-medium">{t("agentList.registerRow")}</span>
									<span className="text-muted-foreground text-sm">{t("agent.registerHint")}</span>
								</span>
								<Button asChild className="w-full sm:w-auto">
									<Link to="/agents/new" search={{ return: REGISTER_RETURN } as never} data-testid="evaluate-register">
										<Plus />
										{t("agent.register")}
									</Link>
								</Button>
							</div>
						) : (
							grid(own, t("agent.yours"), "evaluate-agents-own")
						)}
					</section>
					<section className="flex flex-col gap-3">
						<h3 className="text-sm font-semibold">{t("agent.builtin")}</h3>
						{builtin.length ? (
							grid(builtin, t("agent.builtin"), "evaluate-agents-builtin")
						) : (
							<p className="text-muted-foreground text-sm">{t("agentList.noBuiltIn")}</p>
						)}
					</section>
				</>
			)}

			{/* A plain row, not a boxed card, so the text keeps its width on phones (M3-41). */}
			<section className="flex flex-col gap-3 border-t pt-4 sm:flex-row sm:items-center" data-testid="evaluate-mcp-server">
				<span className="bg-muted text-muted-foreground hidden size-9 shrink-0 items-center justify-center rounded-lg sm:flex">
					<Plug className="size-4" aria-hidden />
				</span>
				<span className="flex min-w-0 flex-1 flex-col gap-0.5">
					<span className="text-sm font-semibold">{t("agent.mcpServer.title")}</span>
					<span className="text-muted-foreground text-sm">{t("agent.mcpServer.hint")}</span>
				</span>
				<Button asChild variant="outline" size="sm" className="w-full sm:w-auto">
					<Link to={"/mcp-test" as never} data-testid="evaluate-mcp-server-link">
						{t("agent.mcpServer.cta")}
					</Link>
				</Button>
			</section>

			{/* "Pick an agent to continue." is said once, in the footer (Q-64). */}
		</div>
	);
}

export default Step1Agent;