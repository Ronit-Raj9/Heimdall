import { AgentCard } from "@/components/agents/agentCard";
import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { useGetAgentsQuery } from "@/lib/store";
import { cn } from "@/lib/utils";
import { AlertTriangle, EyeOff } from "lucide-react";
import { useTranslation } from "react-i18next";

/** The agents screen: every agents/*.yaml config, names only for anything that can hold a secret. */
export default function AgentsView() {
	const { t } = useTranslation("bench");
	const query = useGetAgentsQuery();
	const data = query.data;
	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="agents-screen">
			<header className="flex flex-col gap-1">
				<div className="flex flex-wrap items-center gap-3">
					<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("agents.title")}</h1>
					<ModeBadge mode={data?.mode} isLoading={query.isLoading} isError={query.isError && !data} />
				</div>
				<p className="text-muted-foreground max-w-3xl text-sm">{t("agents.subtitle")}</p>
			</header>
			{data?.mode === "synthetic" && (
				<p
					role="note"
					className="border-chart-warning/50 bg-chart-warning/10 text-foreground flex items-start gap-2 rounded-sm border px-4 py-2 text-sm"
					data-testid="agents-synthetic-banner"
				>
					<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("agents.synthetic")}
				</p>
			)}
			<p
				role="note"
				className="border-chart-info/50 bg-chart-info/10 text-foreground flex items-start gap-2 rounded-sm border px-3 py-2 text-xs"
				data-testid="agents-secret-note"
			>
				<EyeOff className="text-chart-info-ink mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
				{t("agents.secretNote")}
			</p>
			{query.isLoading ? (
				<BlockSkeleton rows={6} testId="agents-loading" />
			) : query.isError || !data ? (
				<LoadError title={t("agents.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="agents-error" />
			) : data.agents.length === 0 ? (
				<EmptyNote testId="agents-empty">{t("agents.empty")}</EmptyNote>
			) : (
				<ul
					className={cn("grid min-w-0 grid-cols-1 gap-3 lg:grid-cols-2 2xl:grid-cols-3", query.isFetching && "opacity-60")}
					aria-label={t("agents.label")}
					data-testid="agents-list"
				>
					{data.agents.map((agent) => (
						<li key={agent.path} className="min-w-0">
							<AgentCard agent={agent} />
						</li>
					))}
				</ul>
			)}
		</section>
	);
}