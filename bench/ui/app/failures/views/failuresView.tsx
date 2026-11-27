import ClustersTab from "@/app/inbox/views/clustersTab";
import { cn } from "@/lib/utils";
import { pageWidthClass } from "@/components/shell/pageWidth";
import FindingsTab from "@/app/inbox/views/findingsTab";
import { InboxTab } from "@/app/inbox/views/inboxView";
import TriageTab from "@/app/inbox/views/triageTab";
import { MaximFailuresLink, PatternsTab } from "@/app/why-agents-fail/views/whyAgentsFailView";
import { ShadowReplayLink } from "@/components/replays/shadowReplayLink";
import { PageHeader, RouteTabs, type RouteTab } from "@/components/shell";
import { useTranslation } from "react-i18next";
import "./ufd2I18n";

/** The tabs of /failures in their order, with the old tab ids of /inbox and /why-agents-fail as aliases. Exported for tests. */
export function failuresTabs(label: (key: string) => string): RouteTab[] {
	return [
		{ id: "production", label: label("production"), aliases: ["inbox"] },
		{ id: "findings", label: label("findings"), aliases: ["tracked"] },
		{ id: "groups", label: label("groups"), aliases: ["clusters"] },
		{ id: "patterns", label: label("patterns"), aliases: ["triage"] },
		{ id: "agents", label: label("agents") },
	];
}

/**
 * Failures (/failures?tab=), one page for what /inbox and /why-agents-fail showed (M7-1):
 * production failures, tracked findings, failure groups across benchmark runs, failure patterns
 * per comparison and per agent version. The old routes redirect here with their query kept.
 */
export default function FailuresView() {
	const { t } = useTranslation("ufd2");
	const tabs = failuresTabs((key) => t(`failures.tabs.${key}` as never));
	return (
		<section className={cn(pageWidthClass("wide"), "flex flex-col gap-6")} data-page-width="wide" data-testid="failures-page">
			<PageHeader
				title={t("failures.title")}
				actions={
					<>
						<MaximFailuresLink />
						<ShadowReplayLink />
					</>
				}
			/>
			<RouteTabs tabs={tabs} defaultTab="production" label={t("failures.tabs.label")} testId="failures-tabs">
				{(active) =>
					active === "findings" ? (
						<FindingsTab />
					) : active === "groups" ? (
						<ClustersTab />
					) : active === "patterns" ? (
						<TriageTab />
					) : active === "agents" ? (
						<PatternsTab />
					) : (
						<InboxTab />
					)
				}
			</RouteTabs>
		</section>
	);
}
