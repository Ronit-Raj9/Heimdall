import { Dialog } from "@/components/ui/dialog";
import { GovDialog } from "@/components/govern/govDialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useStartDebugRunMutation } from "@/lib/store/apis/runDebugApi";
import type { DebugRunResponse, DivergencePolicy } from "@/lib/types/runDebug";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { parseCallList, parseNames } from "./debug.utils";

const textareaClass =
	"border-input bg-background focus-visible:ring-ring/50 min-h-24 w-full rounded-lg border px-3 py-2 text-sm focus-visible:ring-[3px] focus-visible:outline-none";

function errorText(err: unknown): string | undefined {
	if (!err) return undefined;
	const e = err as { data?: { error?: { message?: string } | string; message?: string } };
	const d = e.data;
	if (typeof d?.error === "string") return d.error;
	return d?.error?.message ?? d?.message;
}

/** The result line after a debug run starts: its experiment id and a link to it. */
export function StartedNote({ started }: { started: DebugRunResponse | null }) {
	const { t } = useTranslation("runDebug");
	const openRef = useRef<HTMLAnchorElement>(null);
	// The dialog that started the run has closed: move focus to the new run's link.
	useEffect(() => {
		if (started) openRef.current?.focus();
	}, [started]);
	if (!started) return null;
	return (
		<p className="text-chart-success-ink text-sm" data-testid="debug-started">
			{t("actions.started", { id: started.exp_id })}{" "}
			<Link ref={openRef} className="underline" to="/experiments/$expId" params={{ expId: started.exp_id }}>
				{t("actions.open")}
			</Link>
		</p>
	);
}

/**
 * Starts a debug run from a dialog (N214). On success the dialog hands the started run up and closes,
 * so the page shows the result line and the next Fork opens a fresh dialog; a refused start keeps the
 * dialog open with its error. Answers whether the run started.
 */
export async function startDebugRun(
	run: () => Promise<{ data?: DebugRunResponse; error?: unknown }>,
	onStarted: ((res: DebugRunResponse) => void) | undefined,
	onClose: () => void,
): Promise<boolean> {
	const res = await run();
	if (!res.data) return false;
	onStarted?.(res.data);
	onClose();
	return true;
}

/**
 * Confirms a replay before it starts (M5-2): says what it does, that it is free and never counted, and
 * that it creates a new debug run. Nothing starts until "Start replay"; Cancel starts nothing.
 */
export function ReplayDialog({
	open,
	onClose,
	onConfirm,
	busy,
}: {
	open: boolean;
	onClose(): void;
	onConfirm(): unknown;
	busy?: boolean;
}) {
	const { t } = useTranslation("runDebug");
	return (
		<Dialog
			open={open}
			onOpenChange={(next) => !next && onClose()}
			title={t("actions.replayConfirmTitle")}
			description={t("actions.replayConfirm")}
			closeLabel={t("fork.close")}
			size="sm"
			testId="replay-dialog"
			footer={
				<>
					<Button variant="outline" onClick={onClose} dataTestId="replay-cancel">
						{t("actions.replayCancel")}
					</Button>
					<Button onClick={() => void onConfirm()} disabled={busy} isLoading={busy} dataTestId="replay-start">
						{t("actions.replayStart")}
					</Button>
				</>
			}
		/>
	);
}

type ForkChange = "response" | "message" | "none";

/** Fork from model call N: replay before it, apply an edit at it, then continue live. Start states the cost and is the confirmation. */
export function ForkDialog({
	runId,
	call,
	open,
	onClose,
	onStarted,
}: {
	runId: string;
	call: number;
	open: boolean;
	onClose(): void;
	onStarted?(res: DebugRunResponse): void;
}) {
	const { t } = useTranslation("runDebug");
	const [change, setChange] = useState<ForkChange>("message");
	const [text, setText] = useState("");
	const [divergence, setDivergence] = useState<DivergencePolicy>("live");
	const [start, state] = useStartDebugRunMutation();
	const needsText = change !== "none" && text.trim() === "";
	const submit = async () => {
		const body = {
			call,
			on_divergence: divergence,
			...(change === "response" ? { response: text } : change === "message" ? { message: text } : {}),
		};
		await startDebugRun(() => start({ runId, mode: "fork", body }), onStarted, onClose);
	};
	return (
		<GovDialog
			open={open}
			onClose={onClose}
			title={t("fork.title", { call })}
			description={`${t("fork.description")} ${t("fork.cost", { call })}`}
			closeLabel={t("fork.close")}
			testId="fork-dialog"
		>
			<div className="flex flex-col gap-4">
				<fieldset className="flex flex-col gap-1.5 text-sm">
					<legend className="mb-1 text-xs font-medium">{t("fork.mode")}</legend>
					{(["message", "response", "none"] as const).map((m) => (
						<label key={m} className="inline-flex items-center gap-2">
							<input
								type="radio"
								name="fork-change"
								checked={change === m}
								onChange={() => setChange(m)}
								data-testid={`fork-change-${m}`}
							/>
							{t(m === "message" ? "fork.modeMessage" : m === "response" ? "fork.modeResponse" : "fork.modeNone")}
						</label>
					))}
				</fieldset>
				{change !== "none" && (
					<label className="flex flex-col gap-1.5 text-xs font-medium">
						{t(change === "message" ? "fork.messageLabel" : "fork.responseLabel")}
						<textarea
							className={textareaClass}
							value={text}
							onChange={(e) => setText(e.target.value)}
							placeholder={t(change === "message" ? "fork.messagePlaceholder" : "fork.responsePlaceholder")}
							data-testid="fork-text"
						/>
					</label>
				)}
				<fieldset className="flex flex-col gap-1.5 text-sm">
					<legend className="mb-1 text-xs font-medium">{t("fork.divergence")}</legend>
					{(["live", "stop"] as const).map((d) => (
						<label key={d} className="inline-flex items-center gap-2">
							<input type="radio" name="fork-divergence" checked={divergence === d} onChange={() => setDivergence(d)} />
							{t(d === "live" ? "fork.divergenceLive" : "fork.divergenceStop")}
						</label>
					))}
				</fieldset>
				{state.error ? (
					<p className="text-chart-error-ink text-sm" role="alert" data-testid="fork-error">
						{errorText(state.error) ?? t("actions.failed")}
					</p>
				) : null}
				<div className="flex justify-end gap-2">
					<Button variant="outline" onClick={onClose}>
						{t("fork.cancel")}
					</Button>
					<Button
						onClick={() => void submit()}
						disabled={needsText || state.isLoading}
						isLoading={state.isLoading}
						dataTestId="fork-submit"
					>
						{t("fork.submit")}
					</Button>
				</div>
			</div>
		</GovDialog>
	);
}

/** Rerun live with breakpoints: chosen calls, every call, or right after named tools. */
export function BreakpointDialog({
	runId,
	open,
	onClose,
	onStarted,
	initialCalls = "",
}: {
	runId: string;
	open: boolean;
	onClose(): void;
	onStarted?(res: DebugRunResponse): void;
	initialCalls?: string;
}) {
	const { t } = useTranslation("runDebug");
	const [calls, setCalls] = useState(initialCalls);
	const [every, setEvery] = useState(false);
	const [tools, setTools] = useState("");
	const [start, state] = useStartDebugRunMutation();
	const parsed = parseCallList(calls);
	const names = parseNames(tools);
	const invalid = parsed === null;
	const empty = !invalid && !every && parsed.length === 0 && names.length === 0;
	const submit = async () => {
		if (invalid || empty) return;
		const breakpoints = {
			...(parsed.length ? { calls: parsed } : {}),
			...(every ? { every: true } : {}),
			...(names.length ? { after_tools: names } : {}),
		};
		await startDebugRun(() => start({ runId, mode: "step", body: { breakpoints } }), onStarted, onClose);
	};
	return (
		<GovDialog
			open={open}
			onClose={onClose}
			title={t("debugDialog.title")}
			description={`${t("debugDialog.description")} ${t("debugDialog.cost")}`}
			closeLabel={t("fork.close")}
			testId="breakpoint-dialog"
		>
			<div className="flex flex-col gap-4">
				<label className="flex flex-col gap-1.5 text-xs font-medium">
					{t("debugDialog.calls")}
					<Input
						value={calls}
						onChange={(e) => setCalls(e.target.value)}
						placeholder={t("debugDialog.callsPlaceholder")}
						aria-invalid={invalid}
						data-testid="breakpoint-calls"
					/>
				</label>
				<label className="inline-flex items-center gap-2 text-sm">
					<input type="checkbox" checked={every} onChange={(e) => setEvery(e.target.checked)} data-testid="breakpoint-every" />
					{t("debugDialog.every")}
				</label>
				<label className="flex flex-col gap-1.5 text-xs font-medium">
					{t("debugDialog.tools")}
					<Input
						value={tools}
						onChange={(e) => setTools(e.target.value)}
						placeholder={t("debugDialog.toolsPlaceholder")}
						data-testid="breakpoint-tools"
					/>
				</label>
				{(invalid || empty) && (
					<p className={cn("text-xs", invalid ? "text-chart-error-ink" : "text-muted-foreground")} data-testid="breakpoint-hint">
						{t(invalid ? "debugDialog.invalid" : "debugDialog.needOne")}
					</p>
				)}
				{state.error ? (
					<p className="text-chart-error-ink text-sm" role="alert" data-testid="breakpoint-error">
						{errorText(state.error) ?? t("actions.failed")}
					</p>
				) : null}
				<div className="flex justify-end gap-2">
					<Button variant="outline" onClick={onClose}>
						{t("fork.cancel")}
					</Button>
					<Button
						onClick={() => void submit()}
						disabled={invalid || empty || state.isLoading}
						isLoading={state.isLoading}
						dataTestId="breakpoint-submit"
					>
						{t("debugDialog.submit")}
					</Button>
				</div>
			</div>
		</GovDialog>
	);
}

export { errorText };