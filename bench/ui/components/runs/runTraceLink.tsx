import { BENCH_API_BASE } from "@/lib/store/apis/fixtures";
import { Download } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * Where a run's OpenTelemetry trace downloads from: GET /runs/{id}/otlp (docs/trace-export.md). The run
 * id is encoded, so it can never add a path segment or a query.
 */
export function otlpDownloadUrl(runId: string): string {
	return `${BENCH_API_BASE}/runs/${encodeURIComponent(runId)}/otlp`;
}

export interface TraceDownloadLinkProps {
	runId: string;
	arm: string;
	/** Traces are exported only from a sealed run. */
	sealed: boolean;
	/** Set when the sealed folder no longer matches its seal; the server then refuses the export. */
	sealError?: string;
}

/**
 * A "Download trace (OTLP)" link for one run: the run as an OpenTelemetry trace, to load into Maxim,
 * Langfuse, Jaeger or any OTLP backend. An unsealed run, or one that does not match its seal, gets a
 * note in its place, since the server refuses to export it.
 */
export function TraceDownloadLink({ runId, arm, sealed, sealError }: TraceDownloadLinkProps) {
	const { t } = useTranslation("bench");
	if (!sealed) {
		return (
			<span className="text-muted-foreground text-xs" data-testid={`run-trace-${arm}-unsealed`}>
				{t("runs.trace.unsealed")}
			</span>
		);
	}
	if (sealError) {
		return (
			<span className="text-chart-error-ink text-xs break-words" data-testid={`run-trace-${arm}-seal-mismatch`}>
				{t("runs.trace.sealMismatch")}
			</span>
		);
	}
	return (
		<a
			href={otlpDownloadUrl(runId)}
			download={`${runId}.otlp.json`}
			rel="noopener noreferrer"
			aria-label={t("runs.trace.downloadAria", { run: runId, arm })}
			className="text-primary focus-visible:ring-ring/50 inline-flex w-fit items-center gap-1 rounded-sm text-sm font-medium underline-offset-2 hover:underline focus-visible:ring-[3px] focus-visible:outline-none"
			data-testid={`run-trace-${arm}-download`}
		>
			<Download className="size-3.5" aria-hidden="true" />
			{t("runs.trace.download")}
		</a>
	);
}