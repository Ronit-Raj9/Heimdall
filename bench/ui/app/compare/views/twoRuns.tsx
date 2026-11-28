import "./w4m3I18n";
import { EntityLink } from "@/components/shell/entityLink";
import { BlockSkeleton, LoadError, Section } from "@/components/runs/screenStates";
import { useGetRunQuery } from "@/lib/store/apis/benchApi";
import type { RunDetail } from "@/lib/types/bench";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { RunDiff } from "./runDiff";
import { StepsSideBySide } from "@/components/compare/stepsSideBySide";

/** The pair both runs belong to, or null when they are of different pairs or either is unknown (p06-1). */
export function sharedPair(a: RunDetail | undefined, b: RunDetail | undefined): string | null {
	const pa = a?.run.pair_id ?? "";
	return pa !== "" && pa === (b?.run.pair_id ?? "") ? pa : null;
}

/**
 * /compare?a=<run>&b=<run> (p06-1): two runs side by side. Two runs of one pair open that pair's
 * own comparison through onPair; any other two runs show the run diff with a link to each run.
 */
export function TwoRuns({ a, b, onPair }: { a: string; b: string; onPair: (pairId: string) => void }) {
	const { t } = useTranslation("w4m3");
	const { t: tb } = useTranslation("bench");
	const ra = useGetRunQuery(a);
	const rb = useGetRunQuery(b);
	const pair = sharedPair(ra.data, rb.data);
	useEffect(() => {
		if (pair) onPair(pair);
	}, [pair, onPair]);

	for (const [id, q] of [
		[a, ra],
		[b, rb],
	] as const) {
		if (q.error) {
			return (
				<LoadError
					title={t("twoRuns.notFound", { id })}
					error={q.error}
					testId="compare-two-runs-error"
					notFound={{ text: t("twoRuns.notFound", { id }), backTo: "/runs", backLabel: tb("runs.list.title") }}
				/>
			);
		}
	}
	if (!ra.data || !rb.data) return <BlockSkeleton testId="compare-two-runs-loading" />;
	const lanes = [
		{ label: "A", d: ra.data },
		{ label: "B", d: rb.data },
	];
	return (
		<Section title={t("twoRuns.title")} testId="compare-two-runs">
			<div className="flex flex-wrap gap-4 text-sm">
				{lanes.map((l) => (
					<span key={l.label} className="flex items-center gap-2">
						<span className="text-muted-foreground">{l.label}</span>
						<EntityLink kind="run" id={l.d.run.run_id} />
					</span>
				))}
			</div>
			<RunDiff lanes={lanes.map((l) => ({ arm: l.label, run: l.d.run, metrics: l.d.metrics }))} />
			{ra.data.slot?.task === rb.data.slot?.task && <StepsSideBySide a={a} b={b} />}
		</Section>
	);
}