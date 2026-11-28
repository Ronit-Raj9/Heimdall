import "./ufl2I18n";

import { BlockSkeleton, EmptyNote, InlineError } from "@/components/runs/screenStates";
import { useGetGatewayOverheadQuery } from "@/lib/store/apis/experimentOverheadApi";
import type { OverheadEffect } from "@/lib/types/experimentOverhead";
import { useTranslation } from "react-i18next";

const ms = (v: number) => `${v.toFixed(1)} ms`;

/** GET /experiments/{id}/gateway-overhead on the Cost tab: Bifrost's own time per model call, by plugin. */
export function GatewayOverheadPanel({ expId }: { expId: string }) {
	const { t } = useTranslation("ufl2");
	const q = useGetGatewayOverheadQuery(expId);
	return (
		<section className="flex flex-col gap-2 rounded-lg border px-4 py-3" data-testid="gateway-overhead">
			<h3 className="text-sm font-semibold">{t("overhead.title")}</h3>
			{q.isLoading ? (
				<BlockSkeleton rows={2} testId="gateway-overhead-loading" />
			) : q.isError ? (
				<InlineError error={q.error} title={t("overhead.error")} onRetry={() => void q.refetch()} testId="gateway-overhead-error" />
			) : q.data ? (
				<OverheadBody effect={q.data} />
			) : null}
		</section>
	);
}

export function OverheadBody({ effect }: { effect: OverheadEffect }) {
	const { t } = useTranslation("ufl2");
	const d = effect.details;
	if (!d || d.rows === 0) return <EmptyNote testId="gateway-overhead-empty">{t("overhead.empty")}</EmptyNote>;
	return (
		<div className="flex flex-col gap-2 text-sm">
			<p data-testid="gateway-overhead-headline">{effect.headline}</p>
			{d.median_ms !== undefined && d.p95_ms !== undefined && (
				<dl className="text-muted-foreground flex flex-wrap gap-x-6 gap-y-1 text-xs">
					<div>
						<dt className="inline">{t("overhead.p50")}: </dt>
						<dd className="inline tabular-nums">{ms(d.median_ms)}</dd>
					</div>
					<div>
						<dt className="inline">{t("overhead.p95")}: </dt>
						<dd className="inline tabular-nums">{ms(d.p95_ms)}</dd>
					</div>
					<div>
						<dt className="inline">{t("overhead.calls")}: </dt>
						<dd className="inline tabular-nums">{d.rows}</dd>
					</div>
				</dl>
			)}
			{d.buckets.length > 0 && (
				<details>
					<summary className="cursor-pointer text-xs">{t("overhead.plugin")}</summary>
					<ul className="mt-1 flex flex-col gap-0.5 text-xs" data-testid="gateway-overhead-buckets">
						{d.buckets.map((b) => (
							<li key={b.name} className="flex justify-between gap-4">
								<span className="truncate font-mono">{b.name}</span>
								<span className="tabular-nums">{ms(b.median_us / 1000)}</span>
							</li>
						))}
					</ul>
				</details>
			)}
		</div>
	);
}
