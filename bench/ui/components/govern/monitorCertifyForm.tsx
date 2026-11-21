import { JobRunButton } from "@/components/jobs/jobRunButton";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Splits a comma or space separated id list, dropping blanks and duplicates. */
export function splitIds(s: string): string[] {
	return [...new Set(s.split(/[\s,]+/).filter(Boolean))];
}

/** Starts a monitor.certify job (local servers) from a monitor config path and labelled experiments. */
export function MonitorCertifyForm({ onDone }: { onDone?(): void }) {
	const { t } = useTranslation("govern");
	const [monitor, setMonitor] = useState("");
	const [exps, setExps] = useState("");
	const [fpr, setFpr] = useState("0.01,0.001");
	const [gradeSource, setGradeSource] = useState("original");
	const ids = splitIds(exps);
	const ready = monitor.trim() !== "" && ids.length > 0;
	return (
		<Card data-testid="monitor-certify">
			<CardContent className="flex flex-col gap-3">
				<h2 className="text-sm font-medium">{t("monitors.certifyTitle")}</h2>
				<p className="text-muted-foreground text-xs">{t("monitors.certifyHint")}</p>
				<div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
					<label className="flex flex-col gap-1 text-xs">
						{t("monitors.monitorPath")}
						<Input value={monitor} placeholder="monitors/guard.yaml" onChange={(e) => setMonitor(e.target.value)} data-testid="monitor-certify-path" />
					</label>
					<label className="flex flex-col gap-1 text-xs">
						{t("monitors.expIds")}
						<Input value={exps} placeholder="e_abc123" onChange={(e) => setExps(e.target.value)} data-testid="monitor-certify-exps" />
					</label>
					<label className="flex flex-col gap-1 text-xs">
						{t("monitors.fpr")}
						<Input value={fpr} onChange={(e) => setFpr(e.target.value)} />
					</label>
					<label className="flex flex-col gap-1 text-xs">
						{t("monitors.gradeSource")}
						<Input value={gradeSource} onChange={(e) => setGradeSource(e.target.value)} />
					</label>
				</div>
				{ready ? (
					<div>
						<JobRunButton
							kind="monitor.certify"
							params={{ monitor: monitor.trim(), exp_ids: ids, fpr: fpr.trim(), grade_source: gradeSource.trim() }}
							label={t("monitors.certify")}
							onDone={() => onDone?.()}
						/>
					</div>
				) : (
					<p className="text-muted-foreground text-xs" data-testid="monitor-certify-need">
						{t("monitors.needInput")}
					</p>
				)}
			</CardContent>
		</Card>
	);
}
