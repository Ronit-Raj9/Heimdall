import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { ExperimentRegradeRequest } from "@/lib/types/bench";
import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";

/** The regrade id rule the server enforces (ops.ValidID); "original" and "reviewed" are reserved grade sources. */
const REGRADE_ID = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;
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
	isLoading?: boolean;
	onSubmit(body: ExperimentRegradeRequest): void;
	onClose(): void;
}

/**
 * The Regrade dialog: the options `bifrost-bench regate` takes (which suite lock to check against,
 * a note every report shows, an optional regrade id that becomes the verdict's grade source).
 * Submitting queues the experiment.regate job; the caller shows its log.
 */
export function RegradeDialog({ expId, isLoading, onSubmit, onClose }: RegradeDialogProps) {
	const { t } = useTranslation("platform");
	const [v, setV] = useState<RegradeFormValues>(EMPTY_REGRADE_FORM);
	const titleId = useId();
	const problem = regradeFormProblem(v);
	useEffect(() => {
		const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [onClose]);
	const fieldCls = "flex flex-col gap-1 text-xs";
	return (
		<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" data-testid="regrade-backdrop">
			<form
				role="dialog"
				aria-modal="true"
				aria-labelledby={titleId}
				className="bg-card text-card-foreground w-full max-w-lg space-y-4 rounded-lg border p-5 text-left shadow-lg"
				data-testid="regrade-dialog"
				onSubmit={(e) => {
					e.preventDefault();
					if (!problem) onSubmit(regradeRequest(v));
				}}
			>
				<div>
					<h2 id={titleId} className="text-lg font-semibold">
						{t("experimentActions.regradeDialog.title", { id: expId })}
					</h2>
					<p className="text-muted-foreground text-sm">{t("experimentActions.regradeDialog.intro")}</p>
				</div>
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
				{problem && (v.id || v.lockChoice === "frozen" || v.note) && (
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
		</div>
	);
}
