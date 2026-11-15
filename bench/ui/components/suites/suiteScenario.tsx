import { OutputBadge } from "@/components/scenario/scenarioBadges";
import { isServiceRole } from "@/components/scenario/scenario.utils";
import { formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { ArtifactSpec, ImportRecord, OutputKind, ServiceSpec, SuiteTask } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Ban, Download, EyeOff, Network } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ToneBadge } from "./suiteBadges";
import { DeclaredArtifacts } from "./taskArtifacts";

/** Where an imported suite came from: benchmark, source, license, revision, importer and when. */
export function ImportedFrom({ record }: { record: ImportRecord }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const none = <span className="text-muted-foreground">{t("suites.imported.notRecorded")}</span>;
	const rows: { key: string; label: string; value: React.ReactNode }[] = [
		{ key: "benchmark", label: t("suites.imported.benchmark"), value: record.benchmark || none },
		{ key: "source", label: t("suites.imported.source"), value: record.source || none },
		{
			key: "license",
			label: t("suites.imported.license"),
			value: record.license || <span className="text-chart-warning-ink">{t("suites.licenses.missing")}</span>,
		},
		{ key: "revision", label: t("suites.imported.revision"), value: record.revision || none },
		{ key: "importer", label: t("suites.imported.importer"), value: record.importer || none },
		{ key: "at", label: t("suites.imported.at"), value: formatDateTime(record.at, locale) ?? none },
	];
	return (
		<section className="bg-card flex min-w-0 flex-col gap-2 rounded-sm border px-3 py-2" data-testid="suites-imported">
			<h3 className="text-foreground flex items-center gap-1.5 text-xs font-semibold tracking-wide uppercase">
				<Download className="size-3.5 shrink-0" aria-hidden="true" />
				{t("suites.imported.title")}
			</h3>
			<p className="text-muted-foreground text-xs">{t("suites.imported.description")}</p>
			<dl className="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs" aria-label={t("suites.imported.label")}>
				{rows.map((row) => (
					<div key={row.key} className="contents" data-testid={`suites-imported-${row.key}`}>
						<dt className="text-muted-foreground">{row.label}</dt>
						{/* Upstream text, shown as text: a source URL is never made a link. */}
						<dd className="text-foreground min-w-0 font-mono break-all">{row.value}</dd>
					</div>
				))}
			</dl>
		</section>
	);
}

/** Whether a task has anything for the scenario block: a plain repository task has none. */
export function hasTaskScenario(task: SuiteTask): boolean {
	return Boolean(
		task.environment || task.services?.length || task.sessions?.length || task.output || task.skills?.length || task.impossible,
	);
}

/**
 * A task's scenario facts: its own image, services, sessions with what carries over, output,
 * skills, and the impossible flag explained. Each part shows only when the task declares it;
 * a plain repository task renders nothing. An artifact task without its own artifact list shows
 * the suite's (suiteArtifacts), labelled as the suite's.
 */
export function TaskScenario({
	task,
	suiteOutput,
	suiteArtifacts,
}: {
	task: SuiteTask;
	suiteOutput?: OutputKind;
	suiteArtifacts?: readonly ArtifactSpec[];
}) {
	const { t } = useTranslation("bench");
	const output = task.output?.kind ?? suiteOutput;
	const inherited = output === "artifact" && !task.output?.artifacts?.length && suiteArtifacts?.length ? suiteArtifacts : null;
	if (!hasTaskScenario(task) && !inherited) return null;
	const env = task.environment;
	return (
		<section className="flex min-w-0 flex-col gap-3" data-testid="suites-task-scenario">
			<div className="flex flex-col gap-0.5">
				<h3 className="text-foreground text-xs font-semibold tracking-wide uppercase">{t("suites.scenario.title")}</h3>
				<p className="text-muted-foreground text-xs">{t("suites.scenario.hint")}</p>
			</div>

			{task.impossible && (
				<div
					role="note"
					className="border-chart-error/50 bg-chart-error/10 flex items-start gap-2 rounded-sm border px-3 py-2 text-xs"
					data-testid="suites-task-impossible"
				>
					<Ban className="text-chart-error-ink mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
					<div className="flex min-w-0 flex-col gap-0.5">
						<span className="text-foreground font-medium">{t("suites.scenario.impossible")}</span>
						<span className="text-muted-foreground">{t("suites.scenario.impossibleBody")}</span>
					</div>
				</div>
			)}

			{env && (
				<Part title={t("suites.scenario.image")} hint={t("suites.scenario.imageHint")} testId="suites-task-image">
					<ul className="flex min-w-0 flex-col gap-0.5 text-xs">
						{env.image && <li className="text-foreground font-mono break-all">{env.image}</li>}
						{env.dockerfile && (
							<li className="text-muted-foreground font-mono break-all">
								{t("suites.scenario.builtFrom", { dockerfile: env.dockerfile })}
							</li>
						)}
						{env.workspace_path && (
							<li className="text-muted-foreground font-mono break-all">
								{t("suites.scenario.workspacePath", { path: env.workspace_path })}
							</li>
						)}
						{env.verifier_image && (
							<li className="text-muted-foreground font-mono break-all">
								{t("suites.scenario.verifierImage", { image: env.verifier_image })}
							</li>
						)}
						{env.build_network && <li className="text-muted-foreground">{t("suites.scenario.buildNetwork")}</li>}
					</ul>
				</Part>
			)}

			{task.services && task.services.length > 0 && (
				<Part title={t("suites.scenario.services")} hint={t("suites.scenario.servicesHint")} testId="suites-task-services">
					<ul className="flex min-w-0 flex-col gap-1.5">
						{task.services.map((service) => (
							<ServiceItem key={service.id} service={service} />
						))}
					</ul>
				</Part>
			)}

			{task.sessions && task.sessions.length > 0 && (
				<Part title={t("suites.scenario.sessions")} hint={t("suites.scenario.sessionsHint")} testId="suites-task-sessions">
					<ol className="flex min-w-0 flex-col gap-1.5">
						{task.sessions.map((session, i) => (
							<li
								key={session.id}
								className="flex min-w-0 flex-col gap-0.5 rounded-sm border px-2 py-1.5 text-xs"
								data-testid={`suites-task-session-${session.id}`}
							>
								<span className="text-foreground font-medium">{t("suites.scenario.sessionLabel", { index: i + 1, id: session.id })}</span>
								<span className="text-muted-foreground flex min-w-0 flex-wrap gap-x-3 font-mono text-[11px]">
									<span className="break-all">{t("suites.scenario.instruction", { file: session.instruction })}</span>
									{i > 0 && (
										<span className={session.keep_workspace ? "text-chart-warning-ink" : undefined}>
											{session.keep_workspace ? t("suites.scenario.keepsWorkspace") : t("suites.scenario.resetsWorkspace")}
										</span>
									)}
									{session.files && <span className="break-all">{t("suites.scenario.sessionFiles", { files: session.files })}</span>}
									{session.wall_s ? <span>{t("suites.scenario.sessionWall", { count: session.wall_s })}</span> : null}
								</span>
							</li>
						))}
					</ol>
					<p className="text-muted-foreground text-xs" data-testid="suites-task-persists">
						{t("suites.scenario.persists")}
					</p>
				</Part>
			)}

			{output && (task.output || output !== "diff") && (
				<Part title={t("suites.scenario.output")} testId="suites-task-output">
					<span className="flex min-w-0 flex-wrap items-center gap-2 text-xs">
						<OutputBadge output={output} testId="suites-task-output-kind" />
						{!task.output && <span className="text-muted-foreground">{t("suites.scenario.fromSuite")}</span>}
						{task.output?.answer_path && (
							<span className="text-muted-foreground font-mono break-all">
								{t("suites.scenario.answerPath", { path: task.output.answer_path })}
							</span>
						)}
					</span>
					{task.output?.artifacts && task.output.artifacts.length > 0 && <DeclaredArtifacts artifacts={task.output.artifacts} />}
					{inherited && <DeclaredArtifacts artifacts={inherited} fromSuite />}
				</Part>
			)}

			{task.skills && task.skills.length > 0 && (
				<Part title={t("suites.scenario.skills")} hint={t("suites.scenario.skillsHint")} testId="suites-task-skills">
					<ul className="flex flex-wrap gap-1">
						{task.skills.map((skill) => (
							<li key={skill} className="bg-muted rounded-sm px-1.5 py-0.5 font-mono text-[11px]">
								{skill}
							</li>
						))}
					</ul>
				</Part>
			)}
		</section>
	);
}

function Part({ title, hint, children, testId }: { title: string; hint?: string; children: React.ReactNode; testId: string }) {
	return (
		<div className="flex min-w-0 flex-col gap-1" data-testid={testId}>
			<h4 className="text-foreground text-xs font-medium">{title}</h4>
			{hint && <p className="text-muted-foreground text-xs">{hint}</p>}
			{children}
		</div>
	);
}

/** Tool and MCP services go through the tool proxy unless they opt out; data and user services do not. */
function isProxied(service: ServiceSpec): boolean {
	if (service.proxy !== undefined) return service.proxy;
	return service.role === "tool" || service.role === "mcp";
}

function ServiceItem({ service }: { service: ServiceSpec }) {
	const { t } = useTranslation("bench");
	const proxied = isProxied(service);
	const image = service.image || service.build?.tag || service.build?.dockerfile;
	const exports = (service.export ?? []).map((ex) => ex.id);
	return (
		<li
			className="flex min-w-0 flex-col gap-0.5 rounded-sm border px-2 py-1.5 text-xs"
			data-testid={`suites-task-service-${service.id}`}
			data-proxied={proxied}
		>
			<span className="flex min-w-0 flex-wrap items-center gap-1.5">
				<span className="text-foreground font-mono font-medium">{service.id}</span>
				{service.role && (
					<span className="text-muted-foreground">{isServiceRole(service.role) ? t(`scenario.role.${service.role}`) : service.role}</span>
				)}
				{proxied ? (
					<ToneBadge tone="neutral" icon={Network} hint={t("suites.scenario.proxiedHint")}>
						{t("suites.scenario.proxied")}
					</ToneBadge>
				) : (
					<span className="text-muted-foreground">{t("suites.scenario.direct")}</span>
				)}
				{service.hidden && (
					<ToneBadge tone="neutral" icon={EyeOff} hint={t("suites.scenario.hiddenHint")}>
						{t("suites.scenario.hidden")}
					</ToneBadge>
				)}
			</span>
			<span className={cn("text-muted-foreground flex min-w-0 flex-wrap gap-x-3 font-mono text-[11px]")}>
				{image && <span className="break-all">{image}</span>}
				{service.port ? <span>{t("suites.scenario.port", { port: service.port })}</span> : null}
				{exports.length > 0 && <span className="break-all">{t("suites.scenario.exports", { exports: exports.join(", ") })}</span>}
			</span>
		</li>
	);
}