import { BlockSkeleton } from "@/components/runs/screenStates";
import { useGetVersionExplainQuery } from "@/lib/store/apis/improveApi";
import type { ExplainFile, RoundView, VersionExplain } from "@/lib/types/improve";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import "./w45impI18n";

/** The bundle key of the standing instructions (improve.InstructionsKey). */
export const INSTRUCTIONS_KEY = "@instructions";

/** What changed in a version and why (A4.3): each file's diff, then each edit with its reason and result. */
export function VersionExplainView({ explain: x }: { explain: VersionExplain }) {
	const { t } = useTranslation("w45imp");
	return (
		<div className="flex flex-col gap-3 text-xs" data-testid="version-explain">
			{x.rationale && (
				<div>
					<p className="text-foreground font-medium">{t("explain.rationale")}</p>
					<p className="text-muted-foreground">{x.rationale}</p>
				</div>
			)}
			{x.files.map((f) => (
				<ExplainFileView key={f.path} file={f} />
			))}
			{x.why && <p className="text-foreground">{x.why}</p>}
		</div>
	);
}

function ExplainFileView({ file: f }: { file: ExplainFile }) {
	const { t } = useTranslation("w45imp");
	return (
		<div className="overflow-hidden rounded-md border" data-testid="version-explain-file">
			<div className="bg-muted text-muted-foreground px-2 py-1 font-mono text-[11px]">
				{f.path === INSTRUCTIONS_KEY ? t("form.instructions") : f.path}
			</div>
			{f.lines.length === 0 ? (
				<p className="text-muted-foreground p-2">{t("explain.noLines")}</p>
			) : (
				<pre className="max-h-80 overflow-auto p-2 font-mono text-[11px] leading-5">
					{f.lines.map((l, i) => (
						<div
							key={i}
							className={cn(
								l.op === "+" && "bg-chart-success/10 text-chart-success-ink",
								l.op === "-" && "bg-chart-error/10 text-chart-error-ink",
							)}
						>
							{l.op} {l.text}
						</div>
					))}
				</pre>
			)}
			{f.edits.length > 0 && (
				<ul className="flex flex-col gap-1 border-t p-2">
					{f.edits.map((e, i) => (
						<li key={`${e.id ?? e.op}-${i}`} className="flex flex-col">
							<span className="text-foreground">{t("explain.edit", { op: e.op, reason: e.reason })}</span>
							<span className="text-muted-foreground">
								{[
									e.evidence_count > 0 ? t("explain.evidence", { count: e.evidence_count }) : "",
									e.predicts_fixed > 0 ? t("explain.predicts", { count: e.predicts_fixed }) : "",
									t("explain.result", { result: e.result }),
								]
									.filter(Boolean)
									.join("; ")}
							</span>
						</li>
					))}
				</ul>
			)}
		</div>
	);
}

/** One kept version's explain view, fetched. */
function KeptVersion({ id, version }: { id: string; version: number }) {
	const { t } = useTranslation("w45imp");
	const q = useGetVersionExplainQuery({ id, n: version });
	if (q.isLoading) return <BlockSkeleton rows={3} testId="improvement-explain-loading" />;
	if (q.error || !q.data) return <p className="text-muted-foreground text-xs">{t("explain.error")}</p>;
	return <VersionExplainView explain={q.data} />;
}

/**
 * The "What changed and why" section's body: each kept version against the one before it, from the
 * first change to the final version, then the ideas that were not kept with their reason.
 */
export function VersionExplainSection({ id, versions, rounds }: { id: string; versions: readonly number[]; rounds: readonly RoundView[] }) {
	const { t } = useTranslation("w45imp");
	const rejected = rounds.flatMap((r) => (r.candidates ?? []).filter((c) => c.dropped).map((c) => ({ round: r.n, c })));
	return (
		<div className="flex flex-col gap-3" data-testid="improvement-explain">
			{versions.length === 0 ? (
				<p className="text-muted-foreground text-xs">{t("explain.none")}</p>
			) : (
				versions.map((v) => <KeptVersion key={v} id={id} version={v} />)
			)}
			{rejected.length > 0 && (
				<div className="text-xs">
					<p className="text-foreground font-medium">{t("explain.rejected")}</p>
					<ul className="text-muted-foreground list-disc pl-5">
						{rejected.map(({ round, c }) => (
							<li key={`${round}-${c.id}`}>{t("explain.rejectedRow", { n: round, why: c.dropped })}</li>
						))}
					</ul>
				</div>
			)}
		</div>
	);
}