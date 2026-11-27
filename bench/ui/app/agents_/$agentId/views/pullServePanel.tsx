import { CopyCodeBlock } from "@/components/suites/copyCodeBlock";
import { useGetPullAgentsQuery, type PullAgentStatus } from "@/lib/store/apis/pullAgentsApi";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

/** The name a pull agent serves under: pull.name from its YAML, else the agent id. */
export function pullNameOf(yaml: string, agentId: string): string {
	const m = /^pull:[ \t]*\r?\n((?:[ \t]+.*\r?\n?)*)/m.exec(yaml);
	const name = m && /^[ \t]+name:[ \t]*["']?([^"'\s#]+)["']?/m.exec(m[1]);
	return name ? name[1] : agentId;
}

export function pythonSnippet(name: string, origin: string): string {
	return [
		"import os",
		"from heimdall import serve",
		"",
		"def fn(turn):",
		"    # turn has the task and the chat so far; return your agent's answer",
		"    ...",
		"",
		`serve("${name}", fn, url="${origin}", token=os.environ["HEIMDALL_TOKEN"])`,
	].join("\n");
}

export function typescriptSnippet(name: string, origin: string): string {
	return [
		'import { serve } from "@heimdall/bench-client";',
		"",
		"async function fn(turn) {",
		"  // turn has the task and the chat so far; return your agent's answer",
		"}",
		"",
		`await serve("${name}", fn, { url: "${origin}", token: process.env.HEIMDALL_TOKEN });`,
	].join("\n");
}

/** The status row of name in the list, if any process has served it. */
export function statusFor(list: readonly PullAgentStatus[] | null | undefined, name: string): PullAgentStatus | undefined {
	return (list ?? []).find((a) => a.agent === name);
}

function currentOrigin(): string {
	return typeof window !== "undefined" && window.location ? window.location.origin : "https://your-heimdall";
}

/** Live connection status and the snippets that serve this agent; the status is polled every 10 s. */
export function PullServeStatus({ status, name, origin, loading }: { status?: PullAgentStatus; name: string; origin: string; loading?: boolean }) {
	const { t } = useTranslation("agentBuilder");
	const connected = !!status?.connected;
	const lastSeen = status?.last_poll && !status.last_poll.startsWith("0001-") ? new Date(status.last_poll).toLocaleString() : null;
	return (
		<section className="bg-card flex min-w-0 flex-col gap-4 rounded-xl border p-5" data-testid="pull-serve-panel">
			<div className="flex flex-wrap items-center justify-between gap-2">
				<h3 className="text-sm font-semibold">{t("pull.panel.title")}</h3>
				<span
					className={cn(
						"inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium",
						connected ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300" : "bg-muted text-muted-foreground",
					)}
					data-testid="pull-serve-state"
					data-state={connected ? "connected" : "offline"}
					aria-live="polite"
				>
					<span className={cn("size-1.5 rounded-full", connected ? "bg-emerald-500" : "bg-muted-foreground/50")} aria-hidden="true" />
					{loading ? t("pull.panel.checking") : connected ? t("pull.panel.connected") : t("pull.panel.offline")}
				</span>
			</div>
			<dl className="grid grid-cols-1 gap-2 text-sm sm:grid-cols-3" data-testid="pull-serve-facts">
				<div>
					<dt className="text-muted-foreground text-xs">{t("pull.panel.processes")}</dt>
					<dd className="tabular-nums">{status?.processes ?? 0}</dd>
				</div>
				<div>
					<dt className="text-muted-foreground text-xs">{t("pull.panel.lastSeen")}</dt>
					<dd>{lastSeen ?? t("pull.panel.never")}</dd>
				</div>
				<div>
					<dt className="text-muted-foreground text-xs">{t("pull.panel.queued")}</dt>
					<dd className="tabular-nums">{t("pull.panel.queuedValue", { queued: status?.queued ?? 0, inFlight: status?.in_flight ?? 0 })}</dd>
				</div>
			</dl>
			<p className="text-muted-foreground text-xs">{t("pull.panel.help", { name })}</p>
			<div className="flex flex-col gap-1">
				<span className="text-xs font-medium">Python</span>
				<CopyCodeBlock code={pythonSnippet(name, origin)} label={t("pull.panel.python")} testId="pull-snippet-python" />
			</div>
			<div className="flex flex-col gap-1">
				<span className="text-xs font-medium">TypeScript</span>
				<CopyCodeBlock code={typescriptSnippet(name, origin)} label={t("pull.panel.typescript")} testId="pull-snippet-ts" />
			</div>
			<p className="text-muted-foreground text-xs" data-testid="pull-serve-note">
				{t("pull.panel.refuse")}
			</p>
		</section>
	);
}

/** "Serve this agent from your code" for a pull agent's Setup tab. */
export function PullServePanel({ agentId, yaml }: { agentId: string; yaml: string }) {
	const name = pullNameOf(yaml, agentId);
	const q = useGetPullAgentsQuery(undefined, { pollingInterval: 10_000, skipPollingIfUnfocused: true });
	return <PullServeStatus status={statusFor(q.data?.agents, name)} name={name} origin={currentOrigin()} loading={q.isLoading} />;
}
