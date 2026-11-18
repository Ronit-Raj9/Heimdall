import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { ToneBadge } from "@/components/suites/suiteBadges";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatCount } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import { useGetRunDiffQuery } from "@/lib/store/apis/benchApi";
import { useGetRunCodeScanQuery } from "@/lib/store/apis/codescanApi";
import type { CodeScanReport } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { CircleCheck, ShieldAlert, ShieldX } from "lucide-react";
import { useTranslation } from "react-i18next";
import { SEVERITY_TONE, findingWhere, flaggedFiles, sortFindings, type AnnotatedLine } from "./codescan.utils";

/**
 * X4: one run's code scan, fetched by run id with the run's diff for the annotated view. Loading
 * and errors are shown, never a blank box; a diff that fails to load still leaves the findings
 * table.
 */
export function RunCodeScanPanel({ runId, arm }: { runId: string; arm: string }) {
	const { t } = useTranslation("bench");
	const scan = useGetRunCodeScanQuery(runId);
	const diff = useGetRunDiffQuery(runId);
	if (scan.isLoading) return <BlockSkeleton rows={4} testId={`codescan-${arm}-loading`} />;
	if (scan.error || !scan.data) {
		return (
			<LoadError title={t("codescan.loadError")} error={scan.error} onRetry={() => void scan.refetch()} testId={`codescan-${arm}-error`} />
		);
	}
	return <CodeScanPanel report={scan.data} patch={diff.data?.patch} arm={arm} />;
}

export interface CodeScanPanelProps {
	report: CodeScanReport;
	/** The run's workspace.diff; undefined when it could not be loaded. */
	patch?: string;
	arm: string;
}

/**
 * The security findings on the lines a run's agent added: whether they veto the run under the
 * experiment's level, every finding with its rule, CWE and redacted line, external scanners and
 * their status, and the diff of each flagged file with the flagged lines marked. Every string
 * from the run is rendered as text, never HTML.
 */
export function CodeScanPanel({ report, patch, arm }: CodeScanPanelProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const scan = report.scan;
	if (!scan.scanned) {
		return (
			<EmptyNote testId={`codescan-${arm}-unscanned`}>
				{t("codescan.notScanned", { reason: scan.reason ?? t("codescan.noReason") })}
			</EmptyNote>
		);
	}
	const findings = sortFindings(scan.findings);
	const ignored = new Set(report.ignored_rules ?? []);
	const files = patch !== undefined ? flaggedFiles(patch, scan.findings) : [];
	const failed = (scan.external ?? []).filter((x) => x.error);

	return (
		<div className="flex min-w-0 flex-col gap-4" data-testid={`codescan-${arm}`}>
			<div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
				{report.vetoed ? (
					<ToneBadge tone="bad" icon={ShieldX} testId={`codescan-${arm}-status`}>
						{t("codescan.vetoed", { level: report.veto_at })}
					</ToneBadge>
				) : findings.length > 0 ? (
					<ToneBadge tone="warn" icon={ShieldAlert} testId={`codescan-${arm}-status`}>
						{t("codescan.findings", { count: findings.length, value: formatCount(findings.length, locale) })}
					</ToneBadge>
				) : (
					<ToneBadge tone={failed.length > 0 ? "warn" : "good"} icon={CircleCheck} testId={`codescan-${arm}-status`}>
						{failed.length > 0 ? t("codescan.incomplete") : t("codescan.clean")}
					</ToneBadge>
				)}
				<span className="text-muted-foreground text-xs" data-testid={`codescan-${arm}-scope`}>
					{t("codescan.scope", {
						lines: formatCount(scan.added_lines, locale),
						files: formatCount(scan.files, locale),
						version: scan.rules_version,
					})}
				</span>
				<span className="text-muted-foreground text-xs" data-testid={`codescan-${arm}-source`}>
					{report.sealed ? t("codescan.sealed") : t("codescan.recomputed")}
				</span>
				{report.veto_at && !report.vetoed && (
					<span className="text-muted-foreground text-xs">{t("codescan.vetoLevel", { level: report.veto_at })}</span>
				)}
			</div>

			{(scan.external ?? []).length > 0 && (
				<ul className="flex flex-col gap-1 text-xs" data-testid={`codescan-${arm}-external`}>
					{(scan.external ?? []).map((x) => (
						<li key={x.scanner} className={cn("font-mono", x.error ? "text-chart-warning-ink" : "text-muted-foreground")}>
							{x.error
								? t("codescan.externalFailed", { scanner: x.scanner, error: x.error })
								: t("codescan.externalRan", { scanner: x.scanner, results: x.results, added: x.on_added_lines, exit: x.exit_code })}
						</li>
					))}
				</ul>
			)}

			{findings.length === 0 ? (
				<EmptyNote testId={`codescan-${arm}-empty`}>{t("codescan.none")}</EmptyNote>
			) : (
				<Table data-testid={`codescan-${arm}-table`}>
					<TableHeader>
						<TableRow>
							<TableHead>{t("codescan.col.severity")}</TableHead>
							<TableHead>{t("codescan.col.rule")}</TableHead>
							<TableHead>{t("codescan.col.where")}</TableHead>
							<TableHead>{t("codescan.col.line")}</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{findings.map((f) => (
							<TableRow
								key={`${f.source}-${f.rule}-${f.file}-${f.line}-${f.removed ? "r" : "a"}`}
								data-testid={`codescan-${arm}-row`}
								data-ignored={ignored.has(f.rule)}
							>
								<TableCell>
									<ToneBadge tone={SEVERITY_TONE[f.severity] ?? "neutral"}>
										{t(`codescan.severity.${f.severity}`, { defaultValue: f.severity })}
									</ToneBadge>
								</TableCell>
								<TableCell className="max-w-xs whitespace-normal">
									<div className="flex flex-col gap-0.5">
										<span className="font-mono text-xs">{f.rule}</span>
										<span className="text-muted-foreground text-xs">
											{f.title}
											{f.cwe ? ` (${f.cwe})` : ""}
											{f.source !== "builtin" ? ` ${t("codescan.fromScanner", { scanner: f.source })}` : ""}
											{ignored.has(f.rule) ? ` ${t("codescan.ignored")}` : ""}
										</span>
									</div>
								</TableCell>
								<TableCell className="font-mono text-xs">
									{findingWhere(f)}
									{f.removed && <span className="text-muted-foreground ml-1">{t("codescan.removed")}</span>}
									{f.in_test && <span className="text-muted-foreground ml-1">{t("codescan.inTest")}</span>}
								</TableCell>
								<TableCell className="max-w-md font-mono text-xs break-all whitespace-normal">{f.snippet}</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
			)}

			{findings.length > 0 && patch === undefined && (
				<p className="text-muted-foreground text-xs" data-testid={`codescan-${arm}-nodiff`}>
					{t("codescan.diffMissing")}
				</p>
			)}
			{files.map((file) => (
				<div key={file.path} className="flex min-w-0 flex-col gap-1" data-testid={`codescan-${arm}-file`}>
					<span className="font-mono text-xs font-medium">{file.path}</span>
					<pre className="bench-mono-block max-h-[24rem] overflow-auto py-2" tabIndex={0}>
						<code>
							{file.lines.map((line) => (
								<PatchLine key={line.n} line={line} arm={arm} />
							))}
						</code>
					</pre>
				</div>
			))}
			{(scan.skipped ?? []).length > 0 && (
				<p className="text-muted-foreground text-xs" data-testid={`codescan-${arm}-skipped`}>
					{t("codescan.skipped", { files: (scan.skipped ?? []).map((s) => `${s.file} (${s.reason})`).join(", ") })}
				</p>
			)}
			{scan.reason && <p className="text-chart-warning-ink text-xs">{scan.reason}</p>}
		</div>
	);
}

const LINE_CLASS: Record<AnnotatedLine["kind"], string> = {
	header: "text-muted-foreground font-semibold",
	hunk: "text-chart-info-ink",
	add: "bg-chart-success/10 text-chart-success-ink",
	del: "bg-chart-error/10 text-chart-error-ink",
	ctx: "",
};

/** One diff line as inert text; a flagged line gets a mark and its findings beneath it. */
function PatchLine({ line, arm }: { line: AnnotatedLine; arm: string }) {
	const flagged = line.findings.length > 0;
	return (
		<>
			<span
				className={cn("block px-3 whitespace-pre", LINE_CLASS[line.kind], flagged && "border-chart-error border-l-2 font-semibold")}
				data-testid={flagged ? `codescan-${arm}-flagged-line` : undefined}
				data-patch-line={line.n}
			>
				{line.text.length > 0 ? line.text : " "}
			</span>
			{line.findings.map((f) => (
				<span
					key={`${f.source}-${f.rule}`}
					className="text-chart-error-ink bg-chart-error/5 block px-3 text-[11px] whitespace-pre-wrap"
					data-testid={`codescan-${arm}-annotation`}
				>
					{`^ ${f.severity} ${f.rule}${f.cwe ? ` ${f.cwe}` : ""}: ${f.title}`}
				</span>
			))}
		</>
	);
}