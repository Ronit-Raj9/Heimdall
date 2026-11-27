import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import type { ExperimentRegradeRequest } from "@/lib/types/bench";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import "@/app/experiments_/$expId/views/fw11I18n";

/**
 * The regrade id rule the store enforces (store.ValidRegradeID: ^[a-z0-9_]+$, at most 128 characters);
 * "original" and "reviewed" are reserved grade sources.
 */
const REGRADE_ID = /^[a-z0-9_]{1,128}$/;
const RESERVED_IDS = new Set(["original", "reviewed"]);
/** The server refuses a longer note. */
export const MAX_REGRADE_NOTE = 2000;

/** What the dialog collects: the options of `bifrost-bench regate`. */
export interface RegradeFormValues {
	/** "current": regate against the suite's lock as it is now; "frozen": the suite.lock file the experiment was frozen with. */
	lockChoice: "current" | "frozen";
	suiteLock: string;
	note: string;
	id: string;
}

export const EMPTY_REGRADE_FORM: RegradeFormValues = { lockChoice: "current", suiteLock: "", note: "", id: "" };

/** The first problem with the form (a locale key under experimentActions.regradeDialog), or null. */
export function regradeFormProblem(v: RegradeFormValues): "badId" | "reservedId" | "needLock" | "badLock" | "longNote" | null {
	const id = v.id.trim();
	if (id && RESERVED_IDS.has(id)) return "reservedId";
	if (id && !REGRADE_ID.test(id)) return "badId";
	if (v.lockChoice === "frozen") {
		const lock = v.suiteLock.trim();
		if (!lock) return "needLock";
		if (lock.startsWith("/") || lock.split("/").includes("..")) return "badLock";
	}
	if (v.note.length > MAX_REGRADE_NOTE) return "longNote";
	return null;
}

/**
 * The name the store would accept for id: lower case, "-" and "." turned into "_". Null when that
 * still breaks the rule or changes nothing.
 */
export function suggestRegradeId(id: string): string | null {
	const trimmed = id.trim();
	const next = trimmed.toLowerCase().replace(/[-.]/g, "_");
	return next !== trimmed && REGRADE_ID.test(next) && !RESERVED_IDS.has(next) ? next : null;
}

/** The POST /experiments/{id}/regrade body: only the options the user set. */
export function regradeRequest(v: RegradeFormValues): ExperimentRegradeRequest {
	const body: ExperimentRegradeRequest = {};
	if (v.id.trim()) body.id = v.id.trim();
	if (v.note.trim()) body.note = v.note.trim();
	if (v.lockChoice === "frozen" && v.suiteLock.trim()) body.suite_lock = v.suiteLock.trim();
	return body;
}

export interface RegradeDialogProps {
	expId: string;
	/** The form to open with (tests); empty by default. */
	initial?: RegradeFormValues;
	isLoading?: boolean;
	onSubmit(body: ExperimentRegradeRequest): void;
	onClose(): void;
}

/**
 * The Regrade dialog: the options `bifrost-bench regate` takes (which suite lock to check against,
 * a note every report shows, an optional regrade id that becomes the verdict's grade source).
 * Submitting queues the experiment.regate job; the caller shows its log.
 */
export function RegradeDialog({ expId, initial, isLoading, onSubmit, onClose }: RegradeDialogProps) {
	const { t } = useTranslation("platform");
	const { t: tf } = useTranslation("fw11");
	const [v, setV] = useState<RegradeFormValues>(initial ?? EMPTY_REGRADE_FORM);
	const suggestion = suggestRegradeId(v.id);
	const problem = regradeFormProblem(v);
	const fieldCls = "flex flex-col gap-1 text-xs";
	return (
		// The shared Dialog (Q-31): focus trapped inside, Escape closes, focus returns to the opener.
		<Dialog
			open
			onOpenChange={(open) => !open && onClose()}
			closeLabel={t("experimentActions.regradeDialog.cancel")}
			hideClose
			title={t("experimentActions.regradeDialog.title", { id: expId })}
			description={t("experimentActions.regradeDialog.intro")}
			testId="regrade-dialog"
		>
			<form
				className="space-y-4 text-left"
				onSubmit={(e) => {
					e.preventDefault();
					if (!problem) onSubmit(regradeRequest(v));
				}}
			>
				<fieldset className="flex flex-col gap-2 text-sm">
					<legend className="text-muted-foreground mb-1 text-xs">{t("experimentActions.regradeDialog.lock")}</legend>
					<label className="flex items-start gap-2">
						<input
							type="radio"
							name="regrade-lock"
							checked={v.lockChoice === "current"}
							onChange={() => setV({ ...v, lockChoice: "current" })}
							data-testid="regrade-lock-current"
						/>
						<span>{t("experimentActions.regradeDialog.lockCurrent")}</span>
					</label>
					<label className="flex items-start gap-2">
						<input
							type="radio"
							name="regrade-lock"
							checked={v.lockChoice === "frozen"}
							onChange={() => setV({ ...v, lockChoice: "frozen" })}
							data-testid="regrade-lock-frozen"
						/>
						<span>{t("experimentActions.regradeDialog.lockFrozen")}</span>
					</label>
					{v.lockChoice === "frozen" && (
						<Input
							value={v.suiteLock}
							onChange={(e) => setV({ ...v, suiteLock: e.target.value })}
							placeholder="experiments/frozen/suite.lock"
							aria-label={t("experimentActions.regradeDialog.lockPath")}
							data-testid="regrade-lock-path"
						/>
					)}
				</fieldset>
				<label className={fieldCls}>
					<span className="text-muted-foreground">{t("experimentActions.regradeDialog.note")}</span>
					<textarea
						className="border-input bg-background min-h-20 rounded-md border p-2 text-sm"
						value={v.note}
						maxLength={MAX_REGRADE_NOTE}
						onChange={(e) => setV({ ...v, note: e.target.value })}
						placeholder={t("experimentActions.regradeDialog.notePlaceholder")}
						data-testid="regrade-note"
					/>
				</label>
				<label className={fieldCls}>
					<span className="text-muted-foreground">{t("experimentActions.regradeDialog.id")}</span>
					<Input value={v.id} onChange={(e) => setV({ ...v, id: e.target.value })} placeholder="rg_gates_v2" data-testid="regrade-id" />
					<span className="text-muted-foreground">{t("experimentActions.regradeDialog.idHint")}</span>
				</label>
				{problem === "badId" && (
					<div className="flex flex-wrap items-center gap-2 text-xs" data-testid="regrade-id-problem">
						<span role="alert" className="text-chart-error-ink">
							{tf("regrade.badId", { id: v.id.trim() })}
						</span>
						{suggestion && (
							<Button type="button" variant="outline" size="sm" onClick={() => setV({ ...v, id: suggestion })} dataTestId="regrade-id-fix">
								{tf("regrade.useSuggestion", { id: suggestion })}
							</Button>
						)}
					</div>
				)}
				{problem && problem !== "badId" && (v.id || v.lockChoice === "frozen" || v.note) && (
					<p role="alert" className="text-chart-error-ink text-xs" data-testid="regrade-problem">
						{t(`experimentActions.regradeDialog.problems.${problem}`)}
					</p>
				)}
				<div className="flex justify-end gap-2">
					<Button type="button" variant="ghost" size="sm" onClick={onClose} dataTestId="regrade-cancel">
						{t("experimentActions.regradeDialog.cancel")}
					</Button>
					<Button type="submit" size="sm" disabled={!!problem} isLoading={isLoading} dataTestId="regrade-submit">
						{t("experimentActions.regradeDialog.submit")}
					</Button>
				</div>
			</form>
		</Dialog>
	);
}