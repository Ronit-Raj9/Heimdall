import { Input } from "@/components/ui/input";
import { browserZone, cronProblem, formatRun, nextRuns } from "@/lib/utils/cron";
import { useTranslation } from "react-i18next";
import "./uf7bI18n";

/** How often a check runs: only on request, three presets with a time, or a cron line of the user's. */
export type HowOftenWhen = "manual" | "daily" | "hourly" | "weekly" | "custom";

/** The choices of the "How often" select, in order; "Custom (cron)" is last. */
export const HOW_OFTEN_WHEN: readonly HowOftenWhen[] = ["daily", "hourly", "weekly", "manual", "custom"];

/** The "How often" block's fields. */
export interface HowOftenValue {
	when: HowOftenWhen;
	/** "HH:MM", used by daily and weekly. */
	time: string;
	/** 0 (Sunday) to 6, used by weekly. */
	weekday: number;
	/** The cron line, used only by custom. */
	cron: string;
	/** IANA zone the times are in. */
	timezone: string;
}

/** Every day at 06:00 in the browser's zone. */
export function newHowOften(p: Partial<HowOftenValue> = {}): HowOftenValue {
	return { when: "daily", time: "06:00", weekday: 1, cron: "", timezone: browserZone(), ...p };
}

function hm(time: string): [number, number] {
	const m = /^(\d{1,2}):(\d{2})$/.exec(time.trim());
	if (!m) return [6, 0];
	return [Math.min(23, Number(m[1])), Math.min(59, Number(m[2]))];
}

/** The cron line the block stands for, or null for "Only when I press Check now". */
export function howOftenCron(v: Pick<HowOftenValue, "when" | "time" | "weekday" | "cron">): string | null {
	const [h, m] = hm(v.time);
	switch (v.when) {
		case "manual":
			return null;
		case "hourly":
			return "0 * * * *";
		case "daily":
			return `${m} ${h} * * *`;
		case "weekly":
			return `${m} ${h} * * ${v.weekday}`;
		default:
			return v.cron.trim();
	}
}

/** The block's fields for a saved cron line and zone: a preset when the line matches one, else custom. */
export function howOftenOf(cron: string | undefined, timezone: string | undefined, fallbackZone = browserZone()): HowOftenValue {
	const tz = timezone?.trim() || (cron ? "UTC" : fallbackZone);
	const line = (cron ?? "").trim().replace(/\s+/g, " ");
	if (!cron) return newHowOften({ when: "manual", timezone: tz });
	if (line === "0 * * * *") return newHowOften({ when: "hourly", timezone: tz });
	const pad = (n: string) => n.padStart(2, "0");
	const daily = /^(\d{1,2}) (\d{1,2}) \* \* \*$/.exec(line);
	if (daily && Number(daily[1]) < 60 && Number(daily[2]) < 24)
		return newHowOften({ when: "daily", time: `${pad(daily[2])}:${pad(daily[1])}`, timezone: tz });
	const weekly = /^(\d{1,2}) (\d{1,2}) \* \* ([0-7])$/.exec(line);
	if (weekly && Number(weekly[1]) < 60 && Number(weekly[2]) < 24)
		return newHowOften({ when: "weekly", time: `${pad(weekly[2])}:${pad(weekly[1])}`, weekday: Number(weekly[3]) % 7, timezone: tz });
	return newHowOften({ when: "custom", cron: line, timezone: tz });
}

/** "empty" or "invalid" when the custom cron line cannot be saved, null when the block is fine. */
export function howOftenProblem(v: HowOftenValue): "empty" | "invalid" | null {
	if (v.when !== "custom") return null;
	return cronProblem(v.cron);
}

/** The zones offered: the browser's list when it has one, always with UTC, the browser zone and the current one. */
export function zoneOptions(current: string): string[] {
	let all: string[] = [];
	try {
		const fn = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf;
		all = fn ? fn("timeZone") : [];
	} catch {
		all = [];
	}
	const set = new Set(["UTC", browserZone(), current, ...all].filter(Boolean));
	return [...set];
}

const selectClass = "border-input bg-background h-9 w-full min-w-0 rounded-md border px-2 text-sm";
const WEEKDAY_KEYS = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"] as const;

/**
 * The shared "How often" block of the drift monitor and repeat run forms: presets with a time and a
 * weekday, a custom cron line that is checked as it is typed, a time zone, and the next two runs.
 */
export function HowOften({
	value,
	onChange,
	testIdPrefix,
	allowManual = true,
}: {
	value: HowOftenValue;
	onChange(v: HowOftenValue): void;
	testIdPrefix: string;
	allowManual?: boolean;
}) {
	const { t } = useTranslation("uf7b");
	const patch = (p: Partial<HowOftenValue>) => onChange({ ...value, ...p });
	const choices = HOW_OFTEN_WHEN.filter((w) => allowManual || w !== "manual");
	const cron = howOftenCron(value);
	const problem = howOftenProblem(value);
	const runs = cron && !problem ? nextRuns(cron, value.timezone, 2) : [];
	const label = "text-foreground text-sm font-medium";
	const note = "text-muted-foreground";
	return (
		<div className="flex min-w-0 flex-col gap-2 text-xs" data-testid={`${testIdPrefix}-how-often`}>
			<div className="grid gap-3 sm:grid-cols-2">
				<label className="flex min-w-0 flex-col gap-1">
					<span className={label}>{t("howOften.label")}</span>
					<select
						className={selectClass}
						value={value.when}
						onChange={(e) => patch({ when: e.target.value as HowOftenWhen })}
						data-testid={`${testIdPrefix}-when`}
					>
						{choices.map((w) => (
							<option key={w} value={w}>
								{t(`howOften.option.${w}`)}
							</option>
						))}
					</select>
				</label>
				{value.when !== "manual" && (
					<label className="flex min-w-0 flex-col gap-1">
						<span className={label}>{t("howOften.zone")}</span>
						<select
							className={selectClass}
							value={value.timezone}
							onChange={(e) => patch({ timezone: e.target.value })}
							data-testid={`${testIdPrefix}-zone`}
						>
							{zoneOptions(value.timezone).map((z) => (
								<option key={z} value={z}>
									{z === browserZone() ? t("howOften.yourZone", { zone: z }) : z}
								</option>
							))}
						</select>
					</label>
				)}
				{value.when === "weekly" && (
					<label className="flex min-w-0 flex-col gap-1">
						<span className={label}>{t("howOften.weekday")}</span>
						<select
							className={selectClass}
							value={String(value.weekday)}
							onChange={(e) => patch({ weekday: Number(e.target.value) })}
							data-testid={`${testIdPrefix}-weekday`}
						>
							{WEEKDAY_KEYS.map((k, i) => (
								<option key={k} value={String(i)}>
									{t(`howOften.day.${k}`)}
								</option>
							))}
						</select>
					</label>
				)}
				{(value.when === "daily" || value.when === "weekly") && (
					<label className="flex min-w-0 flex-col gap-1">
						<span className={label}>{t("howOften.time")}</span>
						<Input
							type="time"
							value={value.time}
							onChange={(e) => patch({ time: e.target.value })}
							data-testid={`${testIdPrefix}-time`}
						/>
					</label>
				)}
				{value.when === "custom" && (
					<label className="flex min-w-0 flex-col gap-1">
						<span className={label}>{t("howOften.cron")}</span>
						<Input
							value={value.cron}
							onChange={(e) => patch({ cron: e.target.value })}
							placeholder="0 2 * * *"
							className="font-mono"
							aria-invalid={problem === "invalid" || undefined}
							data-testid={`${testIdPrefix}-cron`}
						/>
					</label>
				)}
			</div>
			{problem === "invalid" && (
				<span className="text-chart-error-ink" role="alert" data-testid={`${testIdPrefix}-cron-problem`}>
					{t("howOften.invalid")}
				</span>
			)}
			{problem === "empty" && <span className={note}>{t("howOften.cronHelp")}</span>}
			{runs.length > 0 && (
				<span className={note} data-testid={`${testIdPrefix}-next-runs`}>
					{value.timezone === "UTC"
						? t("howOften.nextUtc", { list: runs.map((d) => formatRun(d, "UTC")).join(", ") })
						: t("howOften.next", {
								list: runs.map((d) => formatRun(d, value.timezone)).join(", "),
								zone: value.timezone,
								utc: formatRun(runs[0], "UTC"),
							})}
				</span>
			)}
		</div>
	);
}

/** The most a timed check can spend: runs a day after the daily limit, and the cap per day and per month. */
export interface CheckCost {
	runs: number;
	perRun: number;
	day: number;
	month: number;
}

/**
 * The cost line under the budget fields: at most this many runs a day (the schedule's count, cut by
 * the daily limit), each up to perRun, at most `day` a day and `month` over 30 days. Null when the
 * schedule never fires or a limit is missing.
 */
export function checkCost(runsADay: number, perRun: number, perDay: number): CheckCost | null {
	if (!(runsADay > 0) || !(perRun > 0) || !(perDay > 0)) return null;
	const runs = Math.min(Math.ceil(runsADay), Math.floor(perDay / perRun));
	const day = Math.min(perDay, runs * perRun);
	const month = Math.min(day * 30, runsADay * 30 * perRun);
	return { runs, perRun, day, month };
}

/** "$3.00" */
export function usd(n: number): string {
	return `$${n.toFixed(2)}`;
}
