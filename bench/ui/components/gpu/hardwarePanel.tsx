import { ToneBadge } from "@/components/suites/suiteBadges";
import type { HardwareRecord } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

/**
 * X7: the hardware a GPU task's run held (run.json hardware) and the host-side sampler's summary.
 * Renders nothing for a run without a GPU. A sampler that could not run says so; its numbers are
 * then not shown, never shown as zero.
 */
export function HardwarePanel({ hardware, arm }: { hardware?: HardwareRecord; arm: string }) {
	const { t } = useTranslation("bench");
	if (!hardware || !hardware.gpus || hardware.gpus.length === 0) return null;
	const s = hardware.sampler;
	return (
		<div className="flex min-w-0 flex-col gap-2 rounded-md border p-3 text-xs" data-testid={`gpu-hardware-${arm}`}>
			<p className="text-sm font-medium">{t("gpu.title")}</p>
			<p className="text-muted-foreground">{t("gpu.description")}</p>
			<ul className="flex flex-col gap-1">
				{hardware.gpus.map((g) => (
					<li key={g.uuid} className="flex flex-wrap items-center gap-2" data-testid={`gpu-device-${arm}-${g.index}`}>
						<span>
							{t("gpu.device", {
								index: g.index,
								name: g.name,
								memory: g.memory_mb,
								cc: g.compute_cap,
								driver: g.driver,
							})}
						</span>
						{g.cuda_version ? <ToneBadge tone="neutral">{t("gpu.cuda", { version: g.cuda_version })}</ToneBadge> : null}
						<span className="text-muted-foreground font-mono">{g.uuid}</span>
					</li>
				))}
			</ul>
			{hardware.mode ? <span>{t("gpu.mode", { mode: hardware.mode })}</span> : null}
			{s && s.samples > 0 ? (
				<div className="flex flex-wrap gap-3" data-testid={`gpu-sampler-${arm}`}>
					<span>{t("gpu.gpuSeconds", { value: s.gpu_seconds.toFixed(1) })}</span>
					<span>
						{t("gpu.peak", {
							value: Math.round(s.peak_vram_mb),
							baseline: Math.round(s.baseline_mb),
						})}
					</span>
					<span>{t("gpu.util", { value: s.mean_util_pct.toFixed(0) })}</span>
					<span>{t("gpu.samples", { count: s.samples, ms: s.interval_ms })}</span>
				</div>
			) : (
				<span data-testid={`gpu-no-sampler-${arm}`}>{t("gpu.noSampler")}</span>
			)}
			{s?.error ? (
				<ToneBadge tone="warn" testId={`gpu-sampler-error-${arm}`}>
					{t("gpu.samplerError", { error: s.error.slice(0, 200) })}
				</ToneBadge>
			) : null}
		</div>
	);
}