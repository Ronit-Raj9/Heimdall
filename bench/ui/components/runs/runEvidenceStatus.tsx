import type { AdapterFaultEvidence, RunDetail } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";
import { TraceDownloadLink } from "./runTraceLink";
import { TraceQualityDetails } from "./traceQuality";

/**
 * One run's evidence: the quality and the parts the evidence contract expects, the canonical
 * hashes, trace quality, tool calls requested against executed, adapter fault instrumentation and
 * the trace download. Shared by the run page's Evidence tab and the pair comparison.
 */
export function RunEvidenceStatus({ detail, arm }: { detail: RunDetail; arm: string }) {
	const { t } = useTranslation("bench");
	const unknown = t("runs.evidence.unknown");
	const none = t("runs.evidence.none");
	const canonical = detail.canonical;
	const calls = canonical?.tool_calls;
	return (
		<dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-sm" data-testid={`runs-evidence-${arm}`}>
			<Row label={t("runs.evidence.quality")} testId={`runs-evidence-quality-${arm}`}>
				{detail.evidence_contract?.quality ?? unknown}
			</Row>
			<Row label={t("runs.evidence.available")}>{detail.evidence_contract?.available?.join(", ") || none}</Row>
			<Row label={t("runs.evidence.missing")}>{detail.evidence_contract?.missing?.join(", ") || none}</Row>
			<Row label={t("runs.evidence.taskHash")} mono>
				{canonical?.task.task_hash || unknown}
			</Row>
			<Row label={t("runs.evidence.runInputHash")} mono>
				{canonical?.run_input.run_input_hash || unknown}
			</Row>
			<Row label={t("runs.evidence.level")}>{canonical?.evidence_status.level || unknown}</Row>
			<Row label={t("runs.evidence.unknownReasons")}>{canonical?.evidence_status.unknown_reasons?.join(", ") || none}</Row>
			<Row label={t("runs.evidence.tools")}>
				{calls
					? t("runs.evidence.toolsValue", {
							requested: calls.filter((call) => call.requested).length,
							executed: calls.filter((call) => call.executed).length,
						})
					: unknown}
			</Row>
			<div className="col-span-2 flex flex-col gap-2 pt-1">
				{detail.adapter_fault && <AdapterFaultSummary evidence={detail.adapter_fault} />}
				<TraceQualityDetails arm={arm} trace={detail.run.agent.trace_diagnostics || canonical?.agent_trace} />
				<TraceDownloadLink runId={detail.run.run_id} arm={arm} sealed={detail.sealed} sealError={detail.seal_error} />
			</div>
		</dl>
	);
}

function Row({ label, children, mono, testId }: { label: string; children: React.ReactNode; mono?: boolean; testId?: string }) {
	return (
		<>
			<dt className="text-muted-foreground text-xs">{label}</dt>
			<dd className={mono ? "font-mono text-xs break-all" : "text-xs break-words"} data-testid={testId}>
				{children}
			</dd>
		</>
	);
}

function AdapterFaultSummary({ evidence }: { evidence: AdapterFaultEvidence }) {
	const { t } = useTranslation("bench");
	const decisions = evidence.entries.filter((entry) => entry.phase === "decision");
	const applied = evidence.entries.filter((entry) => entry.phase === "applied");
	return (
		<div className="rounded border p-2 text-xs" data-testid="runs-adapter-fault-evidence">
			<div className="font-medium">{evidence.complete ? t("runs.evidence.adapterComplete") : t("runs.evidence.adapterIncomplete")}</div>
			<div>{t("runs.evidence.adapterDecisions", { count: decisions.length })}</div>
			<div>{t("runs.evidence.adapterApplied", { count: applied.length })}</div>
			{evidence.reason && <div className="text-destructive">{evidence.reason}</div>}
		</div>
	);
}