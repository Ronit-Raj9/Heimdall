import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { ToneBadge } from "@/components/suites/suiteBadges";
import { formatBytes } from "@/components/suites/suites.utils";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { getLocale } from "@/lib/i18n";
import { useGetRunServiceLogQuery, useGetRunServicesQuery } from "@/lib/store/apis/servicesApi";
import type { RunService, RunServices } from "@/lib/types/bench";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** One arm's run of a pair that ran services. */
export interface ServiceRun {
	arm: string;
	runId: string;
}

/**
 * X2: the services that ran beside each arm's agent (a compose stack, or a task's tool servers),
 * whether each came up, and the captured output of each compose service. Renders nothing when no
 * run of the pair had services.
 */
export function ServiceLogsSection({ runs }: { runs: ServiceRun[] }) {
	const { t } = useTranslation("bench");
	if (runs.length === 0) return null;
	return (
		<Section title={t("services.title")} description={t("services.description")} testId="runs-services-section">
			<Tabs defaultValue={runs[0].arm}>
				<TabsList>
					{runs.map((run) => (
						<TabsTrigger key={run.arm} value={run.arm} data-testid={`runs-services-tab-${run.arm}`}>
							{t("runs.arm", { arm: run.arm })}
						</TabsTrigger>
					))}
				</TabsList>
				{runs.map((run) => (
					<TabsContent key={run.arm} value={run.arm}>
						<RunServiceLogs runId={run.runId} arm={run.arm} />
					</TabsContent>
				))}
			</Tabs>
		</Section>
	);
}

/** One run's services, fetched by run id. Loading and errors are shown, never a blank box. */
export function RunServiceLogs({ runId, arm }: { runId: string; arm: string }) {
	const { t } = useTranslation("bench");
	const q = useGetRunServicesQuery(runId);
	if (q.isLoading) return <BlockSkeleton rows={3} testId={`services-${arm}-loading`} />;
	if (q.error || !q.data) {
		return <LoadError title={t("services.loadError")} error={q.error} onRetry={() => void q.refetch()} testId={`services-${arm}-error`} />;
	}
	return <ServiceTable report={q.data} arm={arm} />;
}

/** The services table: every string from the run is rendered as text, never HTML. */
export function ServiceTable({ report, arm }: { report: RunServices; arm: string }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	if (report.services.length === 0) {
		return <EmptyNote testId={`services-${arm}-none`}>{t("services.none")}</EmptyNote>;
	}
	return (
		<div className="flex flex-col gap-2" data-testid={`services-${arm}`}>
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>{t("services.col.service")}</TableHead>
						<TableHead>{t("services.col.image")}</TableHead>
						<TableHead>{t("services.col.state")}</TableHead>
						<TableHead>{t("services.col.log")}</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{report.services.map((s) => (
						<ServiceRow key={s.id} service={s} runId={report.run_id} sealed={report.sealed} locale={locale} />
					))}
				</TableBody>
			</Table>
		</div>
	);
}

function ServiceRow({ service, runId, sealed, locale }: { service: RunService; runId: string; sealed: boolean; locale: string }) {
	const { t } = useTranslation("bench");
	const [open, setOpen] = useState(false);
	const hasLog = service.log_bytes !== undefined && service.log_bytes !== null;
	return (
		<>
			<TableRow data-testid={`service-row-${service.id}`}>
				<TableCell className="font-mono text-xs">
					{service.id}
					{service.role && <span className="text-muted-foreground ml-1">({service.role})</span>}
				</TableCell>
				<TableCell className="font-mono text-xs break-all">{service.image}</TableCell>
				<TableCell>
					{service.ready ? (
						<ToneBadge tone="good">{t("services.ready")}</ToneBadge>
					) : (
						<ToneBadge tone="bad">{t("services.notReady")}</ToneBadge>
					)}
					{service.error && <div className="text-destructive mt-1 text-xs break-words">{service.error}</div>}
				</TableCell>
				<TableCell className="text-xs">
					{!hasLog && <span className="text-muted-foreground">{t("services.noLog")}</span>}
					{hasLog && !sealed && <span className="text-muted-foreground">{t("services.unsealed")}</span>}
					{hasLog && sealed && (
						<Button variant="outline" size="sm" onClick={() => setOpen(!open)} data-testid={`service-log-toggle-${service.id}`}>
							{open
								? t("services.hide")
								: t("services.show", {
										size: formatBytes(service.log_bytes ?? 0, locale),
									})}
						</Button>
					)}
				</TableCell>
			</TableRow>
			{open && (
				<TableRow>
					<TableCell colSpan={4}>
						<ServiceLogText runId={runId} service={service.id} />
					</TableCell>
				</TableRow>
			)}
		</>
	);
}

/** One service's log, fetched only when it is opened, shown as preformatted text. */
export function ServiceLogText({ runId, service }: { runId: string; service: string }) {
	const { t } = useTranslation("bench");
	const q = useGetRunServiceLogQuery({ runId, service });
	if (q.isLoading) return <BlockSkeleton rows={2} testId={`service-log-${service}-loading`} />;
	if (q.error || q.data === undefined) {
		return (
			<LoadError title={t("services.logError")} error={q.error} onRetry={() => void q.refetch()} testId={`service-log-${service}-error`} />
		);
	}
	return (
		<pre
			className="bg-muted max-h-96 overflow-auto rounded p-2 font-mono text-[11px] leading-snug whitespace-pre-wrap"
			data-testid={`service-log-${service}`}
		>
			{q.data}
		</pre>
	);
}