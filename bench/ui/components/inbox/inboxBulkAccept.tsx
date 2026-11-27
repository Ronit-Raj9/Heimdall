import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useBulkAcceptInboxItemsMutation } from "@/lib/store/apis/inboxApi";
import type { InboxBulkAcceptRequest, InboxItem, InboxSuite } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useId, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import "@/app/inbox/views/uf7aI18n";
import { isActionable } from "./inbox.utils";

const FIELD =
	"placeholder:text-muted-foreground/70 dark:bg-input/30 border-input focus-visible:border-primary bg-card w-full min-w-0 rounded-md border px-3 py-2 text-sm shadow-xs outline-none disabled:cursor-not-allowed disabled:opacity-50";

export interface BulkAcceptValues {
	ids: string[];
	suite: string;
	recorded: boolean;
	rubric: string;
	switchGrader: boolean;
}

export type BulkAcceptError = "none" | "suite";

export function bulkAcceptErrors(v: BulkAcceptValues): BulkAcceptError[] {
	const out: BulkAcceptError[] = [];
	if (v.ids.length === 0) out.push("none");
	if (v.suite.trim() === "") out.push("suite");
	return out;
}

/** The POST /inbox/accept body. With neither "recorded" nor a rubric the items become drafts. */
export function bulkRequestFrom(v: BulkAcceptValues): InboxBulkAcceptRequest {
	const rubric = v.rubric.trim();
	const guided = v.recorded || rubric !== "";
	return {
		ids: [...v.ids],
		suite: v.suite.trim(),
		...(guided ? { guide: { ...(v.recorded ? { use_recorded: true } : {}), ...(rubric ? { reply_rubric: rubric } : {}) } } : {}),
		...(guided && v.switchGrader ? { switch_grader: true } : {}),
	};
}

/**
 * Several open items into one benchmark with one shared setting: "the recorded sessions were
 * right" for goldens, or one reply rubric. Items the server could not accept are listed with why.
 */
export function InboxBulkAccept({
	items,
	suites,
	disabled,
}: {
	items: readonly InboxItem[];
	suites: readonly InboxSuite[];
	disabled?: boolean;
}) {
	const { t } = useTranslation("inboxGuide");
	const { t: w } = useTranslation("w4m7");
	const { t: f } = useTranslation("uf7a");
	const ids = useId();
	const open = useMemo(() => items.filter(isActionable), [items]);
	const [values, setValues] = useState<BulkAcceptValues>({ ids: [], suite: "", recorded: false, rubric: "", switchGrader: false });
	const [touched, setTouched] = useState(false);
	const [accept, result] = useBulkAcceptInboxItemsMutation();
	const errors = bulkAcceptErrors(values);
	const set = (patch: Partial<BulkAcceptValues>) => setValues((v) => ({ ...v, ...patch }));
	const off = disabled || result.isLoading;
	const resultLine = result.data && (
		<div role="status" className="flex flex-col gap-1 text-sm" data-testid="inbox-bulk-result">
			<p>{t("bulk.result", { accepted: result.data.accepted.length, failed: result.data.failed.length })}</p>
			{result.data.failed.length > 0 && (
				<ul className="text-muted-foreground list-disc pl-5 text-xs break-words" data-testid="inbox-bulk-failed">
					{result.data.failed.map((f) => (
						<li key={f.id}>{t("bulk.failed", { id: f.id, error: f.error })}</li>
					))}
				</ul>
			)}
		</div>
	);

	// Nothing open is left (the last items were just accepted): keep the result line, hide the fields (wave 4, p13-1).
	if (open.length === 0)
		return (
			<div className="flex flex-col gap-3" data-testid="inbox-bulk-accept">
				{resultLine}
				<p className="text-muted-foreground text-sm" data-testid="inbox-bulk-nothing-left">
					{w("bulk.nothingLeft")}
				</p>
			</div>
		);

	return (
		<form
			className="flex flex-col gap-3"
			noValidate
			data-testid="inbox-bulk-accept"
			onSubmit={(e) => {
				e.preventDefault();
				setTouched(true);
				if (off || errors.length > 0) return;
				void accept(bulkRequestFrom(values));
			}}
		>
			<p className="text-muted-foreground text-xs">{t("bulk.hint")}</p>
			<fieldset className="flex flex-col gap-1" disabled={off}>
				<legend className="text-muted-foreground mb-1 text-xs">{t("bulk.items")}</legend>
				<ul className="flex max-h-56 flex-col gap-1 overflow-auto" data-testid="inbox-bulk-items">
					{open.map((item) => {
						const checked = values.ids.includes(item.id);
						return (
							<li key={item.id}>
								<label className="flex items-start gap-2 text-sm">
									<input
										type="checkbox"
										className="mt-1"
										checked={checked}
										onChange={(e) => set({ ids: e.target.checked ? [...values.ids, item.id] : values.ids.filter((id) => id !== item.id) })}
										data-testid={`inbox-bulk-item-${item.id}`}
									/>
									<span className="min-w-0 break-words">
										{item.title}
										<span className="text-muted-foreground ml-2 font-mono text-xs">{item.id}</span>
										{item.golden && <span className="bg-muted ml-2 rounded-sm px-1.5 py-0.5 text-xs">{t("bulk.golden")}</span>}
									</span>
								</label>
							</li>
						);
					})}
				</ul>
				{touched && errors.includes("none") && (
					<p className="text-chart-error-ink text-xs" role="alert" data-testid="inbox-bulk-error-none">
						{t("bulk.none")}
					</p>
				)}
			</fieldset>
			<div className="flex flex-col gap-1">
				<label htmlFor={`${ids}-suite`} className="text-muted-foreground text-xs">
					{t("bulk.suite")}
				</label>
				<select
					id={`${ids}-suite`}
					className={FIELD}
					value={values.suite}
					onChange={(e) => set({ suite: e.target.value })}
					disabled={off}
					aria-invalid={(touched && errors.includes("suite")) || undefined}
					data-testid="inbox-bulk-suite"
				>
					<option value="">{t("bulk.suitePlaceholder")}</option>
					{suites.map((s) => (
						<option key={s.id} value={s.id}>
							{s.id} ({s.template})
						</option>
					))}
				</select>
				{touched && errors.includes("suite") && (
					<p className="text-chart-error-ink text-xs" role="alert" data-testid="inbox-bulk-error-suite">
						{t("bulk.needsSuite")}
					</p>
				)}
			</div>
			<label className="flex items-start gap-2 text-sm">
				<input
					type="checkbox"
					className="mt-1"
					checked={values.recorded}
					disabled={off}
					onChange={(e) => set({ recorded: e.target.checked })}
					data-testid="inbox-bulk-recorded"
				/>
				<span>{t("bulk.recorded")}</span>
			</label>
			<div className="flex flex-col gap-1">
				<label htmlFor={`${ids}-rubric`} className="text-muted-foreground text-xs">
					{t("bulk.rubric")}
				</label>
				<textarea
					id={`${ids}-rubric`}
					className={cn(FIELD, "min-h-16")}
					value={values.rubric}
					disabled={off}
					onChange={(e) => set({ rubric: e.target.value })}
					data-testid="inbox-bulk-rubric"
				/>
				<p className="text-muted-foreground text-xs">{t("rubric.hint")}</p>
			</div>
			<label className="flex items-start gap-2 text-sm">
				<input
					type="checkbox"
					className="mt-1"
					checked={values.switchGrader}
					disabled={off}
					onChange={(e) => set({ switchGrader: e.target.checked })}
					data-testid="inbox-bulk-switch-grader"
				/>
				<span>{t("bulk.switchGrader")}</span>
			</label>
			{!values.recorded && values.rubric.trim() === "" && <p className="text-muted-foreground text-xs">{t("bulk.asDrafts")}</p>}
			{values.ids.length > 0 && values.suite && (
				<p className="text-sm font-medium" data-testid="inbox-bulk-summary">
					{f("bulk.summary", { count: values.ids.length, suite: values.suite })}
				</p>
			)}
			<div>
				<Button type="submit" disabled={off} isLoading={result.isLoading} dataTestId="inbox-bulk-submit">
					{t("bulk.submit", { count: values.ids.length })}
				</Button>
			</div>
			{result.isError && (
				<p role="alert" className="text-chart-error-ink text-sm break-words" data-testid="inbox-bulk-request-error">
					{getBenchErrorMessage(result.error)}
				</p>
			)}
			{resultLine}
		</form>
	);
}