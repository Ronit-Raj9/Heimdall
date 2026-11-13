import type { RunDiff } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

function lineClass(line: string): string {
	if (line.startsWith("+++") || line.startsWith("---") || line.startsWith("diff ") || line.startsWith("index ")) {
		return "text-muted-foreground font-semibold";
	}
	if (line.startsWith("@@")) return "text-chart-info-ink";
	if (line.startsWith("+")) return "bg-chart-success/10 text-chart-success-ink";
	if (line.startsWith("-")) return "bg-chart-error/10 text-chart-error-ink";
	return "";
}

/**
 * A run's exported diff as inert monospace text: React text nodes only, never HTML, so a patch
 * that contains markup shows the markup (strict CSP, DESIGN.md 18).
 */
export function DiffText({ runDiff, arm }: { runDiff: RunDiff; arm: string }) {
	const { t } = useTranslation("bench");
	const { diff, patch } = runDiff;
	const lines = patch.length > 0 ? patch.replace(/\n$/, "").split("\n") : [];

	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid={`diff-${arm}`}>
			{diff.files.length > 0 && (
				<div className="flex flex-col gap-1">
					<span className="text-muted-foreground text-xs font-medium">{t("runs.diff.files")}</span>
					<ul className="flex flex-wrap gap-1.5">
						{diff.files.map((file) => (
							<li key={file.path} className="bg-muted/50 rounded-sm border px-1.5 py-0.5 font-mono text-[11px]">
								<span className="text-muted-foreground mr-1.5">{file.status}</span>
								{file.path}
							</li>
						))}
					</ul>
				</div>
			)}
			{diff.rejected && diff.rejected.length > 0 && (
				<div className="flex flex-col gap-1" data-testid={`diff-${arm}-rejected`}>
					<span className="text-muted-foreground text-xs font-medium">{t("runs.diff.rejected")}</span>
					<ul className="flex flex-wrap gap-1.5">
						{diff.rejected.map((reject) => (
							<li key={reject.path} className="border-chart-warning/50 rounded-sm border px-1.5 py-0.5 font-mono text-[11px]">
								<span className="text-chart-warning-ink mr-1.5">{reject.reason}</span>
								{reject.path}
							</li>
						))}
					</ul>
				</div>
			)}
			{lines.length === 0 ? (
				<p
					className="text-muted-foreground rounded-sm border border-dashed px-3 py-4 text-center text-xs"
					data-testid={`diff-${arm}-empty`}
				>
					{t("runs.diff.empty")}
				</p>
			) : (
				<pre
					className="bg-muted/30 max-h-[28rem] overflow-auto rounded-sm border py-2 font-mono text-xs leading-5"
					data-testid={`diff-${arm}-patch`}
					tabIndex={0}
				>
					<code>
						{lines.map((line, i) => (
							// Lines repeat, so the position is the only stable key.
							<span key={`line-${i}`} className={cn("block px-3 whitespace-pre", lineClass(line))}>
								{line.length > 0 ? line : " "}
							</span>
						))}
					</code>
				</pre>
			)}
		</div>
	);
}