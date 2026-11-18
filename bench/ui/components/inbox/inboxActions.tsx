import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { readWriteToken, saveWriteToken } from "@/lib/store/apis/benchApi";
import {
	INBOX_TASK_KINDS,
	type InboxAcceptRequest,
	type InboxItem,
	type InboxRejectRequest,
	type InboxSnoozeRequest,
	type InboxSuite,
	type Mode,
} from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Info } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
	SNOOZE_DEFAULT_DAYS,
	SNOOZE_MAX_DAYS,
	SNOOZE_MIN_DAYS,
	acceptDefaults,
	acceptFormErrors,
	acceptRequestFrom,
	canWriteInbox,
	isActionable,
	isInboxState,
	isInboxTaskKind,
	parseSnoozeDays,
	rejectFormErrors,
	rejectRequestFrom,
	snoozeRequestFrom,
	type AcceptFormValues,
	type RejectFormValues,
} from "./inbox.utils";
import { formatInboxTime, useKindLabel } from "./inboxBadges";

const FIELD =
	"placeholder:text-muted-foreground/70 dark:bg-input/30 border-input focus-visible:border-primary bg-card w-full min-w-0 rounded-md border px-3 py-2 text-sm shadow-xs outline-none disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive";

export type InboxAction = "accept" | "reject" | "snooze";
const ACTIONS: readonly InboxAction[] = ["accept", "reject", "snooze"];

// ---------------------------------------------------------------------------------------------
// Accept
// ---------------------------------------------------------------------------------------------

export interface AcceptFormProps {
	item: Pick<InboxItem, "id" | "suggested_kind" | "suggested_suite">;
	suites: readonly InboxSuite[];
	disabled?: boolean;
	submitting?: boolean;
	initialValues?: Partial<AcceptFormValues>;
	/** Show missing-field errors before the first submit, as after a submit attempt. */
	showErrors?: boolean;
	onSubmit: (body: InboxAcceptRequest) => void;
}

/** Accept: the suite the task goes into, the task kind (suggested kind first) and an optional task id. */
export function AcceptForm({ item, suites, disabled, submitting, initialValues, showErrors, onSubmit }: AcceptFormProps) {
	const { t } = useTranslation("bench");
	const kindLabel = useKindLabel();
	const ids = useId();
	const [values, setValues] = useState<AcceptFormValues>(() => ({ ...acceptDefaults(item, suites), ...initialValues }));
	const [touched, setTouched] = useState(Boolean(showErrors));
	const errors = acceptFormErrors(values);
	const shown = touched ? errors : [];
	const set = (patch: Partial<AcceptFormValues>) => setValues((v) => ({ ...v, ...patch }));
	const off = disabled || submitting || suites.length === 0;

	return (
		<form
			className="flex flex-col gap-3"
			noValidate
			data-testid="inbox-accept-form"
			onSubmit={(e) => {
				e.preventDefault();
				setTouched(true);
				if (off || errors.length > 0) return;
				onSubmit(acceptRequestFrom(values));
			}}
		>
			{suites.length === 0 && (
				<p className="text-muted-foreground text-sm" data-testid="inbox-accept-no-suites">
					{t("inbox.actions.acceptForm.noSuites")}
				</p>
			)}
			<Field label={t("inbox.actions.acceptForm.suite")} htmlFor={`${ids}-suite`}>
				<select
					id={`${ids}-suite`}
					className={FIELD}
					value={values.suite}
					onChange={(e) => set({ suite: e.target.value })}
					disabled={off}
					required
					aria-invalid={shown.includes("suite") || undefined}
					data-testid="inbox-accept-suite"
				>
					<option value="">{t("inbox.actions.acceptForm.suitePlaceholder")}</option>
					{suites.map((suite) => (
						<option key={suite.id} value={suite.id}>
							{t("inbox.actions.acceptForm.suiteOption", { suite: suite.id, template: suite.template })}
						</option>
					))}
				</select>
				{shown.includes("suite") && (
					<FieldError testId="inbox-accept-error-suite">{t("inbox.actions.acceptForm.suiteRequired")}</FieldError>
				)}
			</Field>
			<Field
				label={t("inbox.actions.acceptForm.kind")}
				htmlFor={`${ids}-kind`}
				hint={t("inbox.actions.acceptForm.kindHint", { kind: kindLabel(item.suggested_kind) })}
			>
				<select
					id={`${ids}-kind`}
					className={FIELD}
					value={values.kind}
					onChange={(e) => {
						if (isInboxTaskKind(e.target.value)) set({ kind: e.target.value });
					}}
					disabled={off}
					data-testid="inbox-accept-kind"
				>
					{INBOX_TASK_KINDS.map((kind) => (
						<option key={kind} value={kind}>
							{kindLabel(kind)}
						</option>
					))}
				</select>
			</Field>
			<Field label={t("inbox.actions.acceptForm.taskId")} htmlFor={`${ids}-task`}>
				<Input
					id={`${ids}-task`}
					className="font-mono"
					placeholder={t("inbox.actions.acceptForm.taskIdPlaceholder")}
					value={values.taskId}
					onChange={(e) => set({ taskId: e.target.value })}
					disabled={off}
					autoComplete="off"
					spellCheck={false}
					data-testid="inbox-accept-task-id"
				/>
			</Field>
			<Button type="submit" disabled={off} isLoading={submitting} dataTestId="inbox-accept-submit">
				{t("inbox.actions.acceptForm.submit")}
			</Button>
		</form>
	);
}

// ---------------------------------------------------------------------------------------------
// Reject
// ---------------------------------------------------------------------------------------------

export interface RejectFormProps {
	itemId: string;
	disabled?: boolean;
	submitting?: boolean;
	initialValues?: Partial<RejectFormValues>;
	showErrors?: boolean;
	onSubmit: (body: InboxRejectRequest) => void;
}

/** Reject: a required reason, and optionally the item this one duplicates. */
export function RejectForm({ itemId, disabled, submitting, initialValues, showErrors, onSubmit }: RejectFormProps) {
	const { t } = useTranslation("bench");
	const ids = useId();
	const [values, setValues] = useState<RejectFormValues>({ reason: "", duplicateOf: "", ...initialValues });
	const [touched, setTouched] = useState(Boolean(showErrors));
	const errors = rejectFormErrors(values, itemId);
	const shown = touched ? errors : [];
	const set = (patch: Partial<RejectFormValues>) => setValues((v) => ({ ...v, ...patch }));
	const off = disabled || submitting;

	return (
		<form
			className="flex flex-col gap-3"
			noValidate
			data-testid="inbox-reject-form"
			onSubmit={(e) => {
				e.preventDefault();
				setTouched(true);
				if (off || errors.length > 0) return;
				onSubmit(rejectRequestFrom(values));
			}}
		>
			<Field label={t("inbox.actions.rejectForm.reason")} htmlFor={`${ids}-reason`}>
				<textarea
					id={`${ids}-reason`}
					rows={3}
					className={FIELD}
					placeholder={t("inbox.actions.rejectForm.reasonPlaceholder")}
					value={values.reason}
					onChange={(e) => set({ reason: e.target.value })}
					disabled={off}
					required
					aria-invalid={shown.includes("reason") || undefined}
					data-testid="inbox-reject-reason"
				/>
				{shown.includes("reason") && (
					<FieldError testId="inbox-reject-error-reason">{t("inbox.actions.rejectForm.reasonRequired")}</FieldError>
				)}
			</Field>
			<Field label={t("inbox.actions.rejectForm.duplicateOf")} htmlFor={`${ids}-dup`} hint={t("inbox.actions.rejectForm.duplicateHint")}>
				<Input
					id={`${ids}-dup`}
					className="font-mono"
					placeholder={t("inbox.actions.rejectForm.duplicatePlaceholder")}
					value={values.duplicateOf}
					onChange={(e) => set({ duplicateOf: e.target.value })}
					disabled={off}
					autoComplete="off"
					spellCheck={false}
					aria-invalid={shown.includes("duplicateSelf") || undefined}
					data-testid="inbox-reject-duplicate"
				/>
				{shown.includes("duplicateSelf") && (
					<FieldError testId="inbox-reject-error-duplicate">{t("inbox.actions.rejectForm.duplicateSelf")}</FieldError>
				)}
			</Field>
			<Button type="submit" variant="outline" disabled={off} isLoading={submitting} dataTestId="inbox-reject-submit">
				{t("inbox.actions.rejectForm.submit")}
			</Button>
		</form>
	);
}

// ---------------------------------------------------------------------------------------------
// Snooze
// ---------------------------------------------------------------------------------------------

export interface SnoozeFormProps {
	disabled?: boolean;
	submitting?: boolean;
	initialDays?: string;
	showErrors?: boolean;
	onSubmit: (body: InboxSnoozeRequest) => void;
}

/** Snooze: a whole number of days from 1 to 90. */
export function SnoozeForm({ disabled, submitting, initialDays, showErrors, onSubmit }: SnoozeFormProps) {
	const { t } = useTranslation("bench");
	const ids = useId();
	const [days, setDays] = useState(initialDays ?? String(SNOOZE_DEFAULT_DAYS));
	const [touched, setTouched] = useState(Boolean(showErrors));
	const invalid = parseSnoozeDays(days) === null;
	const off = disabled || submitting;

	return (
		<form
			className="flex flex-col gap-3"
			noValidate
			data-testid="inbox-snooze-form"
			onSubmit={(e) => {
				e.preventDefault();
				setTouched(true);
				if (off || invalid) return;
				onSubmit(snoozeRequestFrom(days));
			}}
		>
			<Field label={t("inbox.actions.snoozeForm.days")} htmlFor={`${ids}-days`} hint={t("inbox.actions.snoozeForm.daysHint")}>
				<Input
					id={`${ids}-days`}
					type="number"
					inputMode="numeric"
					min={SNOOZE_MIN_DAYS}
					max={SNOOZE_MAX_DAYS}
					step={1}
					className="w-32"
					value={days}
					onChange={(e) => setDays(e.target.value)}
					disabled={off}
					aria-invalid={(touched && invalid) || undefined}
					data-testid="inbox-snooze-days"
				/>
				{touched && invalid && <FieldError testId="inbox-snooze-error-days">{t("inbox.actions.snoozeForm.daysInvalid")}</FieldError>}
			</Field>
			<Button type="submit" variant="outline" disabled={off} isLoading={submitting} dataTestId="inbox-snooze-submit">
				{t("inbox.actions.snoozeForm.submit")}
			</Button>
		</form>
	);
}

// ---------------------------------------------------------------------------------------------
// The actions block
// ---------------------------------------------------------------------------------------------

export interface InboxActionsProps {
	item: InboxItem;
	suites: readonly InboxSuite[];
	/** The server mode; only live takes changes, so the forms are read-only otherwise. */
	mode: Mode | undefined;
	submitting?: boolean;
	submitError?: string;
	/** The item a reject or snooze returned, to confirm what happened. */
	done?: { action: "reject" | "snooze"; item: InboxItem };
	initialAction?: InboxAction;
	showErrors?: boolean;
	onAccept: (body: InboxAcceptRequest) => void;
	onReject: (body: InboxRejectRequest) => void;
	onSnooze: (body: InboxSnoozeRequest) => void;
}

/**
 * Accept, reject or snooze one item, with the write token every change needs. Only new and snoozed
 * items take actions; on a recorded or synthetic server the forms stay visible but disabled.
 */
export function InboxActions(props: InboxActionsProps) {
	const { t } = useTranslation("bench");
	const ids = useId();
	const [action, setAction] = useState<InboxAction>(props.initialAction ?? "accept");
	const [token, setToken] = useState(readWriteToken);
	const writable = canWriteInbox(props.mode);
	const { item } = props;
	const stateLabel = isInboxState(item.state) ? t(`inbox.state.${item.state}`).toLowerCase() : item.state;

	return (
		<div className="flex flex-col gap-4" data-testid="inbox-actions">
			{props.done && <DoneNote done={props.done} />}
			{!isActionable(item) ? (
				<p className="text-muted-foreground text-sm" role="note" data-testid="inbox-actions-closed">
					{t("inbox.actions.closed", { state: stateLabel })}
				</p>
			) : (
				<>
					{!writable && (
						<p
							className="border-border bg-muted flex items-start gap-2 rounded-sm border px-3 py-2 text-sm"
							role="note"
							data-testid="inbox-actions-read-only"
						>
							<Info className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
							{t("inbox.actions.readOnly", { mode: props.mode ?? "unknown" })}
						</p>
					)}
					<div role="group" aria-label={t("inbox.actions.label")} className="flex flex-wrap gap-2" data-testid="inbox-action-switch">
						{ACTIONS.map((a) => (
							<Button
								key={a}
								type="button"
								size="sm"
								variant={a === action ? "default" : "outline"}
								aria-pressed={a === action}
								onClick={() => setAction(a)}
								dataTestId={`inbox-action-${a}`}
							>
								{t(`inbox.actions.${a}`)}
							</Button>
						))}
					</div>
					<Field label={t("inbox.actions.token")} htmlFor={`${ids}-token`} hint={t("inbox.actions.tokenHint")}>
						<Input
							id={`${ids}-token`}
							type="password"
							autoComplete="off"
							placeholder={t("inbox.actions.tokenPlaceholder")}
							value={token}
							onChange={(e) => {
								setToken(e.target.value);
								saveWriteToken(e.target.value);
							}}
							disabled={!writable}
							data-testid="inbox-token-input"
						/>
					</Field>
					{action === "accept" && (
						<AcceptForm
							key={`accept-${item.id}`}
							item={item}
							suites={props.suites}
							disabled={!writable}
							submitting={props.submitting}
							showErrors={props.showErrors}
							onSubmit={props.onAccept}
						/>
					)}
					{action === "reject" && (
						<RejectForm
							key={`reject-${item.id}`}
							itemId={item.id}
							disabled={!writable}
							submitting={props.submitting}
							showErrors={props.showErrors}
							onSubmit={props.onReject}
						/>
					)}
					{action === "snooze" && (
						<SnoozeForm
							key={`snooze-${item.id}`}
							disabled={!writable}
							submitting={props.submitting}
							showErrors={props.showErrors}
							onSubmit={props.onSnooze}
						/>
					)}
				</>
			)}
			{props.submitError && (
				<p role="alert" className="text-chart-error-ink text-sm break-words" data-testid="inbox-action-error">
					{t("inbox.actions.error", { error: props.submitError })}
				</p>
			)}
		</div>
	);
}

function DoneNote({ done }: { done: NonNullable<InboxActionsProps["done"]> }) {
	const { t } = useTranslation("bench");
	let text: string;
	if (done.action === "snooze") text = t("inbox.actions.done.snoozed", { at: formatInboxTime(done.item.snoozed_until) });
	else if (done.item.duplicate_of) text = t("inbox.actions.done.duplicate", { id: done.item.duplicate_of });
	else text = t("inbox.actions.done.rejected");
	return (
		<p className="text-sm" role="status" data-testid={`inbox-done-${done.action}`}>
			{text}
		</p>
	);
}

function Field({ label, htmlFor, hint, children }: { label: string; htmlFor: string; hint?: string; children: ReactNode }) {
	return (
		<div className="flex flex-col gap-1">
			<label htmlFor={htmlFor} className="text-muted-foreground text-xs">
				{label}
			</label>
			{children}
			{hint && <p className="text-muted-foreground text-xs">{hint}</p>}
		</div>
	);
}

function FieldError({ children, testId }: { children: ReactNode; testId: string }) {
	return (
		<p className={cn("text-chart-error-ink text-xs")} role="alert" data-testid={testId}>
			{children}
		</p>
	);
}