import "@/components/settings/ufl8I18n";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { InlineError } from "@/components/runs/screenStates";
import { notify } from "@/lib/notify";
import { useUpdateLiveMonitorMutation, type MonitorUpdate } from "@/lib/store/apis/liveMonitorEditApi";
import type { MonitorDetailResponse } from "@/lib/store/apis/liveMonitorsApi";
import { useState } from "react";
import { useTranslation } from "react-i18next";

export interface EditFields {
	name: string;
	tasks: string;
	interval: string;
	budget: string;
	minDrop: string;
	floor: string;
}

const pct = (v: unknown) => (typeof v === "number" && v > 0 ? String(Math.round(v * 1000) / 10) : "");

export function editFieldsOf(
	d: Pick<MonitorDetailResponse, "name" | "tasks" | "interval_min" | "budget_usd_per_day" | "rule">,
	taskCount: number,
): EditFields {
	return {
		name: d.name,
		tasks: String(taskCount),
		interval: String(d.interval_min),
		budget: String(d.budget_usd_per_day),
		minDrop: pct(d.rule?.min_drop),
		floor: pct(d.rule?.min_pass_rate),
	};
}

/**
 * The PUT body: only changed fields; the rule goes whole (the server replaces it) with the two edited
 * thresholds as fractions. null when a number is out of range. Exported for tests.
 */
export function monitorUpdateBody(
	d: Pick<MonitorDetailResponse, "rule">,
	before: EditFields,
	f: EditFields,
	minInterval: number,
): MonitorUpdate | null {
	const whole = (v: string) => (/^\d+$/.test(v.trim()) ? Number(v) : NaN);
	const share = (v: string) => (v.trim() === "" ? 0 : Number(v) / 100);
	const tasks = whole(f.tasks);
	const interval = whole(f.interval);
	const budget = Number(f.budget);
	const drop = share(f.minDrop);
	const floor = share(f.floor);
	if (!f.name.trim() || !(tasks >= 1) || !(interval >= minInterval) || !(budget > 0)) return null;
	if (!(drop >= 0 && drop <= 1) || !(floor >= 0 && floor <= 1)) return null;
	const body: MonitorUpdate = {};
	if (f.name.trim() !== before.name) body.name = f.name.trim();
	if (f.tasks !== before.tasks) body.tasks = tasks;
	if (f.interval !== before.interval) body.interval_min = interval;
	if (f.budget !== before.budget) body.budget_usd_per_day = budget;
	if (f.minDrop !== before.minDrop || f.floor !== before.floor) {
		const rule = { ...(d.rule ?? {}) } as Record<string, unknown>;
		delete rule.min_drop;
		delete rule.min_pass_rate;
		if (drop > 0) rule.min_drop = drop;
		if (floor > 0) rule.min_pass_rate = floor;
		body.rule = rule;
	}
	return body;
}

export function EditLiveMonitor({ d, minInterval, onClose }: { d: MonitorDetailResponse; minInterval: number; onClose: () => void }) {
	const { t } = useTranslation("ufl8");
	const [before] = useState(() => editFieldsOf(d, d.tasks.length || 1));
	const [f, setF] = useState(before);
	const [save, state] = useUpdateLiveMonitorMutation();
	const body = monitorUpdateBody(d, before, f, minInterval);
	const field = (k: keyof EditFields, type = "number") => (
		<label className="flex min-w-0 flex-col gap-1 text-xs">
			<span className="text-muted-foreground">{t(`liveEdit.${k}`)}</span>
			<Input type={type} value={f[k]} onChange={(e) => setF({ ...f, [k]: e.target.value })} data-testid={`live-monitor-edit-${k}`} />
		</label>
	);
	const submit = async () => {
		if (!body) return;
		if (Object.keys(body).length === 0) return onClose();
		try {
			await save({ id: d.id, body }).unwrap();
			notify({ text: t("liveEdit.saved") });
			onClose();
		} catch {
			// state.error renders below
		}
	};
	return (
		<div className="flex min-w-0 flex-col gap-3 rounded-md border p-3" data-testid="live-monitor-edit">
			<span className="text-sm font-medium">{t("liveEdit.title")}</span>
			<div className="grid min-w-0 gap-3 sm:grid-cols-3">
				{field("name", "text")}
				{field("tasks")}
				{field("interval")}
				{field("budget")}
				{field("minDrop")}
				{field("floor")}
			</div>
			<p className="text-muted-foreground text-xs">{body ? t("liveEdit.fixed") : t("liveEdit.bad", { min: minInterval })}</p>
			<InlineError error={state.error} testId="live-monitor-edit-error" />
			<div className="flex flex-wrap gap-2">
				<Button
					size="sm"
					disabled={!body || state.isLoading}
					isLoading={state.isLoading}
					onClick={() => void submit()}
					data-testid="live-monitor-edit-save"
				>
					{t("liveEdit.save")}
				</Button>
				<Button size="sm" variant="ghost" onClick={onClose}>
					{t("liveEdit.cancel")}
				</Button>
			</div>
		</div>
	);
}