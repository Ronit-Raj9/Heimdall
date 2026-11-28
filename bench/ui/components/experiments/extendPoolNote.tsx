import "./ufn6I18n";

import { Link } from "@tanstack/react-router";
import { Layers } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useGetExperimentExtensionsQuery } from "@/lib/store/apis/extendPoolApi";
import type { ExtendPoolView } from "@/lib/types/extendPool";

const pct = (x: number) => Math.round(x * 100);
const pts = (x: number) => `${x > 0 ? "+" : ""}${pct(x)}`;

function ExpLink({ id, name }: { id: string; name?: string }) {
	return (
		<Link to="/experiments/$expId" params={{ expId: id }} className="text-primary font-medium hover:underline">
			{name || id}
		</Link>
	);
}

/** Round 6 N6: the parent's pooled result with its Extend follow-ups, or on a follow-up where its runs count. */
export function ExtendPoolView({ view }: { view: ExtendPoolView }) {
	const { t } = useTranslation("ufn6");
	const box = "bg-muted/60 text-foreground flex flex-wrap items-start gap-2 rounded-lg border px-4 py-3 text-sm";
	if (view.parent) {
		return (
			<p className={box} role="note" data-testid="extend-pool-child">
				<Layers className="text-muted-foreground mt-0.5 size-4 shrink-0" aria-hidden="true" />
				{view.parent_pooled ? (
					<span>
						{t("pool.childPooled")} <ExpLink id={view.parent} />
					</span>
				) : (
					<span>{t("pool.childNotPooled", { parent: view.parent, reason: view.parent_reason ?? "" })}</span>
				)}
			</p>
		);
	}
	if (view.extensions.length === 0) return null;
	const pooled = view.extensions.filter((m) => m.pooled);
	const left = view.extensions.filter((m) => !m.pooled);
	const p = view.pooled;
	return (
		<div className={box} role="note" data-testid="extend-pool">
			<Layers className="text-muted-foreground mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<div className="flex min-w-0 flex-col gap-1">
				{p && pooled.length > 0 && (
					<>
						<span data-testid="extend-pool-includes">
							{t("pool.includes", { count: p.added_runs })}{" "}
							{pooled.map((m, i) => (
								<span key={m.exp_id}>
									{i > 0 && ", "}
									<ExpLink id={m.exp_id} name={m.name} />
								</span>
							))}
						</span>
						<span data-testid="extend-pool-result">
							{t("pool.result", { tasks: p.tasks, runs: p.runs, a: pct(p.mean_a), b: pct(p.mean_b), d: pts(p.d_bar) })}
							{p.interval && ` ${t("pool.range", { lo: pts(p.interval[0]), hi: pts(p.interval[1]) })}`}
						</span>
					</>
				)}
				{left.map((m) => (
					<span key={m.exp_id} className="text-muted-foreground break-words" data-testid="extend-pool-refused">
						<ExpLink id={m.exp_id} name={m.name} /> {t("pool.notPooled", { reason: m.reason ?? "" })}
					</span>
				))}
			</div>
		</div>
	);
}

export function ExtendPoolNote({ expId }: { expId: string }) {
	const q = useGetExperimentExtensionsQuery(expId);
	if (!q.data || q.data.exp_id !== expId) return null;
	return <ExtendPoolView view={q.data} />;
}