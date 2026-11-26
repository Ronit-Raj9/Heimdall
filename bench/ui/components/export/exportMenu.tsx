import { IS_FIXTURE_MODE, getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { experimentExportUrl, suiteHarborUrl, useGetExperimentRunTableQuery } from "@/lib/store/apis/exportApi";
import { Download } from "lucide-react";
import { useState, type MouseEvent } from "react";
import { downloadChecked } from "@/lib/downloadChecked";
import "@/app/suites_/$suiteId/views/w4m6I18n";
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
	/**
	 * Synthetic results (scripted agent or model, smoke): the EEE link asks for them labelled. Null
	 * while the server's classification is unknown: the EEE link waits, since an unlabelled request
	 * for synthetic results is refused (409).
	 */
	synthetic?: boolean | null;
	/** The classification request failed: said in place of the EEE link. */
	classifyError?: unknown;
	/** Sealed runs counted, when known (shown in the summary line). */
	runs?: number | null;
	open?: boolean;
	onToggle?: (open: boolean) => void;
}

/** The experiment Export menu: Every Eval Ever records, the run table as JSONL or CSV, traces. */
export function ExportMenu({ expId, synthetic = false, classifyError, runs = null, open, onToggle }: ExperimentExportMenuProps) {
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
				{synthetic === null ? (
					<li className="flex min-w-0 flex-col gap-0.5" data-testid="export-eee-pending">
						<span className="text-muted-foreground inline-flex items-center gap-1 text-sm font-medium">
							<Download className="size-3.5" aria-hidden="true" />
							{t("export.eee")}
						</span>
						<span
							className={classifyError ? "text-chart-error-ink text-xs" : "text-muted-foreground text-xs"}
							role={classifyError ? "alert" : undefined}
						>
							{classifyError ? t("export.eeeClassifyError", { error: getBenchErrorMessage(classifyError) }) : t("export.eeeClassifying")}
						</span>
					</li>
				) : (
					<ExportLink
						href={experimentExportUrl(expId, "eee", synthetic)}
						download={`${expId}.eee.jsonl`}
						label={t("export.eee")}
						note={synthetic ? t("export.eeeSyntheticNote") : t("export.eeeNote")}
						testId="export-eee"
					/>
				)}
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
					{t("export.traces")} <code className="font-mono">{`heimdall export otlp -exp ${expId} -to langfuse`}</code>
				</li>
			</ul>
		</details>
	);
}

/**
 * The experiment Export menu. Once opened it reads the run table, to count the sealed runs and to learn
 * whether they are synthetic, so the EEE link asks for synthetic results labelled only when they are.
 */
export function ExperimentExportMenu({
	expId,
	fixtureMode = IS_FIXTURE_MODE,
	initiallyOpen = false,
}: {
	expId: string;
	fixtureMode?: boolean;
	/** Start open (the comparison's Export tab), so the downloads show without a click (N70). */
	initiallyOpen?: boolean;
}) {
	const [open, setOpen] = useState(initiallyOpen);
	const q = useGetExperimentRunTableQuery(expId, { skip: !open || fixtureMode });
	// Fixture mode has no server to download from, so the menu is not offered (6-C-08).
	if (fixtureMode) return null;
	// Unknown until the run table answers: the EEE link is offered only once the server classified the runs.
	const synthetic = q.data ? q.data.some((row) => row.synthetic) : null;
	return (
		<ExportMenu
			expId={expId}
			synthetic={synthetic}
			classifyError={q.error}
			runs={q.data ? q.data.length : null}
			open={open}
			onToggle={setOpen}
		/>
	);
}

/**
 * The suite Export menu: the admitted public tasks as Harbor task folders, verifiers withheld. The
 * download is checked first (p12-1): a refusal shows its sentence here instead of saving an error
 * file under the tar's name. inline renders the list with no disclosure, for a tab whose section
 * already has the title (N70). The command line export sits in its own "From a terminal" block (N78).
 */
export function SuiteExportMenu({
	suiteId,
	inline = false,
	fixtureMode = IS_FIXTURE_MODE,
}: {
	suiteId: string;
	inline?: boolean;
	fixtureMode?: boolean;
}) {
	const { t } = useTranslation("bench");
	const { t: t6 } = useTranslation("w4m6");
	const [problem, setProblem] = useState<string | null>(null);
	const [busy, setBusy] = useState(false);
	if (fixtureMode) return null;
	const href = suiteHarborUrl(suiteId);
	const filename = `${suiteId}.harbor.tar.gz`;
	const onDownload = async (e: MouseEvent<HTMLAnchorElement>) => {
		e.preventDefault();
		if (busy) return;
		setBusy(true);
		setProblem(null);
		setProblem(await downloadChecked(href, filename));
		setBusy(false);
	};
	const list = (
		<ul className={inline ? "flex flex-col gap-2" : "mt-2 flex flex-col gap-2"}>
			<li className="flex min-w-0 flex-col gap-0.5">
				<a
					href={href}
					download={filename}
					rel="noopener noreferrer"
					className={linkClass}
					aria-disabled={busy || undefined}
					onClick={(e) => void onDownload(e)}
					data-testid="export-harbor"
				>
					<Download className="size-3.5" aria-hidden="true" />
					{t("export.harbor")}
				</a>
				<span className="text-muted-foreground text-xs">{t("export.harborNote")}</span>
				{problem && (
					<p role="alert" className="text-destructive text-xs" data-testid="export-harbor-error">
						{problem}
					</p>
				)}
			</li>
			<li className="text-muted-foreground">
				<details data-testid="export-harbor-cli">
					<summary className="cursor-pointer">{t6("export.terminal")}</summary>
					<p className="mt-1">
						{t("export.harborCli")}{" "}
						<code className="font-mono">{`heimdall export harbor -suite ${suiteId} -out harbor/ -include-hidden`}</code>
					</p>
				</details>
			</li>
		</ul>
	);
	if (inline) {
		return (
			<div className="flex min-w-0 flex-col gap-2 text-xs" data-testid="suite-export-menu">
				{list}
			</div>
		);
	}
	return (
		<details className="flex min-w-0 flex-col gap-2 rounded-md border p-3 text-xs" data-testid="suite-export-menu">
			<summary className="cursor-pointer text-sm font-medium">{t("export.title")}</summary>
			{list}
		</details>
	);
}