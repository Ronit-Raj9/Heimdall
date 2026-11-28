import "@/components/runs/a2a/ufl4I18n";
import { Button } from "@/components/ui/button";
import { downloadChecked } from "@/lib/downloadChecked";
import { BENCH_API_BASE } from "@/lib/store/apis/fixtures";
import { withOrgParam } from "@/lib/store/apis/orgParam";
import { Download } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Run files GET /runs/{id}/files/{name} serves (handlers/runs.go runFileTypes). */
export const RUN_FILES = ["run.json", "grade.json", "metrics.json", "cost.json", "gates.json", "receipts.json", "events.jsonl", "workspace.diff", "seal.json"] as const;

/** Raw evidence GET /runs/{id}/evidence/{name} serves from a sealed run (handlers/runs.go rawEvidenceNameOK). */
export const RAW_EVIDENCE = ["gateway.requests.jsonl", "gateway.raw.jsonl", "tools.jsonl", "proxy.ledger.jsonl", "agent.stream.jsonl", "agent.stderr.txt"] as const;

export function runFileUrl(runId: string, kind: "files" | "evidence", name: string): string {
	return withOrgParam(`${BENCH_API_BASE}/runs/${encodeURIComponent(runId)}/${kind}/${encodeURIComponent(name)}`);
}

/** Downloads of a run's files and, for a sealed run, its raw evidence. A refusal shows the server's reason. */
export function RunFileDownloads({ runId, sealed }: { runId: string; sealed: boolean }) {
	const { t } = useTranslation("ufl4");
	const [error, setError] = useState<string | null>(null);
	const [busy, setBusy] = useState<string | null>(null);
	const get = async (kind: "files" | "evidence", name: string) => {
		setBusy(name);
		setError(null);
		const msg = await downloadChecked(runFileUrl(runId, kind, name), `${runId}.${name}`);
		setBusy(null);
		if (msg) setError(t("files.failed", { name, reason: msg }));
	};
	const row = (kind: "files" | "evidence", names: readonly string[]) => (
		<div className="flex flex-wrap gap-2">
			{names.map((n) => (
				<Button key={n} size="sm" variant="outline" disabled={busy !== null} onClick={() => void get(kind, n)} data-testid={`run-${kind}-${n}`}>
					<Download className="size-3.5" aria-hidden="true" />
					{n}
				</Button>
			))}
		</div>
	);
	return (
		<div className="flex min-w-0 flex-col gap-3 text-sm" data-testid="run-file-downloads">
			<p className="font-medium">{t("files.runFiles")}</p>
			{row("files", RUN_FILES)}
			<p className="font-medium">{t("files.raw")}</p>
			{sealed ? row("evidence", RAW_EVIDENCE) : <p className="text-muted-foreground text-xs">{t("files.unsealed")}</p>}
			{error ? (
				<p role="alert" className="text-chart-error-ink text-xs break-words" data-testid="run-file-error">
					{error}
				</p>
			) : null}
		</div>
	);
}
