import { experimentExportUrl, suiteHarborUrl, useGetExperimentRunTableQuery } from "@/lib/store/apis/exportApi";
import { Download } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * W10 export (docs/export.md): the Export menu of the experiment and suite pages. Each entry is a
 * download from the bench API; nothing is sent anywhere else. Traces go to observability backends from
 * the CLI (export otlp -to <backend>), which needs credentials the browser never sees.
 */

const linkClass =
	"text-primary focus-visible:ring-ring/50 inline-flex w-fit items-center gap-1 rounded-sm text-sm font-medium underline-offset-2 hover:underline focus-visible:ring-[3px] focus-visible:outline-none";

interface ExportLinkProps {
	href: string;
	download: string;
	label: string;
	note: string;
	testId: string;
}

function ExportLink({ href, download, label, note, testId }: ExportLinkProps) {
	return (
		<li className="flex min-w-0 flex-col gap-0.5">
			<a href={href} download={download} rel="noopener noreferrer" className={linkClass} data-testid={testId}>
				<Download className="size-3.5" aria-hidden="true" />
				{label}
			</a>
			<span className="text-muted-foreground text-xs">{note}</span>
		</li>
	);
}

export interface ExperimentExportMenuProps {
	expId: string;
	/** Synthetic results (scripted agent or model, smoke): the EEE link asks for them labelled. */
	synthetic?: boolean;
	/** Sealed runs counted, when known (shown in the summary line). */
	runs?: number | null;
	open?: boolean;
	onToggle?: (open: boolean) => void;
}

/** The experiment Export menu: Every Eval Ever records, the run table as JSONL or CSV, traces. */
export function ExportMenu({ expId, synthetic = false, runs = null, open, onToggle }: ExperimentExportMenuProps) {
	const { t } = useTranslation("bench");
	return (
		<details
			className="flex min-w-0 flex-col gap-2 rounded-md border p-3 text-xs"
			data-testid="export-menu"
			open={open}
			onToggle={(e) => onToggle?.((e.currentTarget as HTMLDetailsElement).open)}
		>
			<summary className="cursor-pointer text-sm font-medium" data-testid="export-menu-summary">
				{t("export.title")}
				{runs !== null ? <span className="text-muted-foreground ml-2 font-normal">{t("export.runs", { count: runs })}</span> : null}
			</summary>
			<ul className="mt-2 flex flex-col gap-2">
				<ExportLink
					href={experimentExportUrl(expId, "eee", synthetic)}
					download={`${expId}.eee.jsonl`}
					label={t("export.eee")}
					note={synthetic ? t("export.eeeSyntheticNote") : t("export.eeeNote")}
					testId="export-eee"
				/>
				<ExportLink
					href={experimentExportUrl(expId, "jsonl")}
					download={`${expId}.runs.jsonl`}
					label={t("export.jsonl")}
					note={t("export.tableNote")}
					testId="export-jsonl"
				/>
				<ExportLink
					href={experimentExportUrl(expId, "csv")}
					download={`${expId}.runs.csv`}
					label={t("export.csv")}
					note={t("export.tableNote")}
					testId="export-csv"
				/>
				<li className="text-muted-foreground" data-testid="export-traces">
					{t("export.traces")} <code className="font-mono">{`bifrost-bench export otlp -exp ${expId} -to langfuse`}</code>
				</li>
			</ul>
		</details>
	);
}

/**
 * The experiment Export menu. Once opened it reads the run table, to count the sealed runs and to learn
 * whether they are synthetic, so the EEE link asks for synthetic results labelled only when they are.
 */
export function ExperimentExportMenu({ expId }: { expId: string }) {
	const [open, setOpen] = useState(false);
	const q = useGetExperimentRunTableQuery(expId, { skip: !open });
	const synthetic = q.data ? q.data.some((row) => row.synthetic) : false;
	return <ExportMenu expId={expId} synthetic={synthetic} runs={q.data ? q.data.length : null} open={open} onToggle={setOpen} />;
}

/** The suite Export menu: the admitted public tasks as Harbor task folders, verifiers withheld. */
export function SuiteExportMenu({ suiteId }: { suiteId: string }) {
	const { t } = useTranslation("bench");
	return (
		<details className="flex min-w-0 flex-col gap-2 rounded-md border p-3 text-xs" data-testid="suite-export-menu">
			<summary className="cursor-pointer text-sm font-medium">{t("export.title")}</summary>
			<ul className="mt-2 flex flex-col gap-2">
				<ExportLink
					href={suiteHarborUrl(suiteId)}
					download={`${suiteId}.harbor.tar.gz`}
					label={t("export.harbor")}
					note={t("export.harborNote")}
					testId="export-harbor"
				/>
				<li className="text-muted-foreground" data-testid="export-harbor-cli">
					{t("export.harborCli")}{" "}
					<code className="font-mono">{`bifrost-bench export harbor -suite ${suiteId} -out harbor/ -include-hidden`}</code>
				</li>
			</ul>
		</details>
	);
}