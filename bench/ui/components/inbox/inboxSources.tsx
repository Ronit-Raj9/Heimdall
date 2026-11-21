import { EmptyNote } from "@/components/runs/screenStates";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { InboxSourceStatus } from "@/lib/types/bench";
import { Button } from "@/components/ui/button";
import { Info, Radar } from "lucide-react";
import { FeatureHero, revealAnchor } from "./featureHero";
import { useTranslation } from "react-i18next";
import { formatInboxTime } from "./inboxBadges";

/** Each trace source and how its last pull went: when, from where on, how many traces, the last error. */
export function InboxSourcesTable({ sources }: { sources: readonly InboxSourceStatus[] }) {
	const { t } = useTranslation("bench");
	if (sources.length === 0) return <EmptyNote testId="inbox-sources-none">{t("inbox.sources.none")}</EmptyNote>;
	return (
		<div className="min-w-0 overflow-x-auto rounded-sm border">
			<Table aria-label={t("inbox.sources.label")} data-testid="inbox-sources-table">
				<TableHeader>
					<TableRow>
						<TableHead>{t("inbox.sources.columns.name")}</TableHead>
						<TableHead>{t("inbox.sources.columns.kind")}</TableHead>
						<TableHead>{t("inbox.sources.columns.lastPull")}</TableHead>
						<TableHead>{t("inbox.sources.columns.since")}</TableHead>
						<TableHead className="text-right">{t("inbox.sources.columns.pulled")}</TableHead>
						<TableHead>{t("inbox.sources.columns.lastError")}</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{sources.map((source) => (
						<TableRow key={source.name} data-testid={`inbox-source-${source.name}`}>
							<TableCell className="font-mono text-xs">{source.name}</TableCell>
							<TableCell className="text-xs">{source.kind}</TableCell>
							<TableCell className="text-xs whitespace-nowrap">
								{source.last_pull ? formatInboxTime(source.last_pull) : t("inbox.sources.never")}
							</TableCell>
							<TableCell className="text-xs whitespace-nowrap">{source.since ? formatInboxTime(source.since) : ""}</TableCell>
							<TableCell className="text-right text-xs tabular-nums">{source.pulled ?? ""}</TableCell>
							<TableCell className="max-w-96 text-xs">
								{source.last_error ? (
									<span className="text-chart-error-ink break-words" data-testid={`inbox-source-error-${source.name}`}>
										{source.last_error}
									</span>
								) : (
									<span className="text-muted-foreground">{t("inbox.sources.ok")}</span>
								)}
							</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
		</div>
	);
}

/** Says plainly that push ingest is off, so items come only from pulls. */
export function InboxIngestOffNote() {
	const { t } = useTranslation("bench");
	return (
		<p
			className="border-border bg-muted flex items-start gap-2 rounded-sm border px-3 py-2 text-sm"
			role="note"
			data-testid="inbox-ingest-off"
		>
			<Info className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
			{t("inbox.sources.ingestOff")}
		</p>
	);
}

/** The empty inbox: one sentence, a "Connect a trace source" action that opens the setup, and the CLI command. */
export function InboxEmpty() {
	const { t: u } = useTranslation("ux13");
	return (
		<FeatureHero
			icon={Radar}
			title={u("inbox.heroTitle")}
			message={u("inbox.heroMessage")}
			testId="inbox-empty"
			action={
				<Button asChild>
					<a
						href="#inbox-source-setup"
						onClick={(e) => {
							e.preventDefault();
							revealAnchor("inbox-source-setup");
						}}
						data-testid="inbox-hero-connect"
					>
						{u("inbox.heroAction")}
					</a>
				</Button>
			}
		>
			<span>{u("inbox.cli")}</span>
			<code className="bg-muted text-foreground rounded-md px-2 py-1 font-mono">heimdall inbox pull</code>
		</FeatureHero>
	);
}
