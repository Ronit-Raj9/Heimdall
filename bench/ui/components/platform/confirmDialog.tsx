import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { formatUsd } from "@/lib/money";
import { CircleDollarSign } from "lucide-react";
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** Options for one confirmation: labels default to the platform catalog's confirmDialog keys. */
export interface ConfirmOptions {
	title?: string;
	confirmLabel?: string;
	cancelLabel?: string;
	/**
	 * Paints the confirm button red, for deletes and other changes that cannot be undone. Give a
	 * title that names the thing ("Delete secret prod-key?"): the generic title is only a fallback.
	 */
	destructive?: boolean;
	/**
	 * For paid work: the most it can spend (the cap the server enforces). null means unknown (it
	 * uses the person's model key); undefined means the action is not paid.
	 */
	costUsd?: number | null;
	/** One sentence under the cost line, for example the estimate ("About 24 runs, about $1.20."). */
	costLine?: string;
}

/** A destructive confirmation must name what it deletes: the type a page lane moves to (X-104). */
export type DestructiveConfirmOptions = ConfirmOptions & { destructive: true; title: string };

/** A confirmation that is not destructive. */
export type SafeConfirmOptions = Omit<ConfirmOptions, "destructive"> & { destructive?: false };

/** What `confirm` takes (X-104): a destructive one must name what it deletes in its title. */
export type StrictConfirmOptions = SafeConfirmOptions | DestructiveConfirmOptions;

export interface ConfirmFn {
	(message: string, opts?: StrictConfirmOptions): Promise<boolean>;
}

interface PendingConfirm {
	message: string;
	opts: ConfirmOptions;
	resolve(ok: boolean): void;
}

/**
 * The queue behind the dialog, kept apart from React so tests can drive it in Node. ask() returns
 * a promise the dialog settles; a second ask() while one is open settles the first as cancelled.
 */
export function createConfirmStore(onChange: (pending: PendingConfirm | null) => void) {
	let pending: PendingConfirm | null = null;
	const settle = (ok: boolean) => {
		const cur = pending;
		pending = null;
		onChange(null);
		cur?.resolve(ok);
	};
	return {
		ask(message: string, opts: ConfirmOptions = {}): Promise<boolean> {
			if (pending) settle(false);
			return new Promise<boolean>((resolve) => {
				pending = { message, opts, resolve };
				onChange(pending);
			});
		},
		settle,
		/** The dialog's keyboard rule: Escape cancels. Returns true when the key was handled. */
		onKey(key: string): boolean {
			if (!pending || key !== "Escape") return false;
			settle(false);
			return true;
		},
		current: () => pending,
	};
}

type ConfirmStore = ReturnType<typeof createConfirmStore>;

const ConfirmContext = createContext<ConfirmFn | null>(null);

// Without a provider (component tests rendering a single view) every confirmation is accepted.
const acceptAll: ConfirmFn = () => Promise.resolve(true);

/** The in-page replacement for window.confirm: `if (!(await confirm(msg))) return;`. */
export function useConfirm(): ConfirmFn {
	return useContext(ConfirmContext) ?? acceptAll;
}

/** Mounted once at the app root; renders the dialog when a screen asks for a confirmation. */
export function ConfirmProvider({ children }: { children: ReactNode }) {
	const [pending, setPending] = useState<PendingConfirm | null>(null);
	const storeRef = useRef<ConfirmStore | null>(null);
	if (!storeRef.current) storeRef.current = createConfirmStore(setPending);
	const store = storeRef.current;
	const confirm: ConfirmFn = useCallback((message: string, opts?: ConfirmOptions) => store.ask(message, opts), [store]);
	return (
		<ConfirmContext.Provider value={confirm}>
			{children}
			{pending && (
				<ConfirmDialog
					message={pending.message}
					opts={pending.opts}
					onConfirm={() => store.settle(true)}
					onCancel={() => store.settle(false)}
					onKey={(key) => store.onKey(key)}
				/>
			)}
		</ConfirmContext.Provider>
	);
}

export interface ConfirmDialogProps {
	message: string;
	opts?: ConfirmOptions;
	onConfirm(): void;
	onCancel(): void;
	onKey?(key: string): boolean;
}

/** The cost sentence of a paid confirmation, or null when the action is not paid. */
export function costSentence(t: (k: string, o?: Record<string, unknown>) => string, costUsd: number | null | undefined): string | null {
	if (costUsd === undefined) return null;
	return costUsd === null ? t("confirm.bodyUnknown", { ns: "paid" }) : t("confirm.body", { ns: "paid", cap: formatUsd(costUsd) });
}

/**
 * The modal itself: an alertdialog through the shared Dialog (focus trapped, app behind inert),
 * focus on Confirm; Escape cancels in the capture phase so a dialog that opened it stays open.
 * The backdrop does not cancel (an alertdialog), Cancel and Escape do.
 */
export function ConfirmDialog({ message, opts = {}, onConfirm, onCancel, onKey }: ConfirmDialogProps) {
	const { t } = useTranslation("platform");
	const okRef = useRef<HTMLButtonElement>(null);
	useEffect(() => {
		const handle = (e: KeyboardEvent) => {
			const handled = onKey ? onKey(e.key) : e.key === "Escape" && (onCancel(), true);
			if (handled) {
				e.preventDefault();
				e.stopPropagation();
			}
		};
		window.addEventListener("keydown", handle, true);
		return () => window.removeEventListener("keydown", handle, true);
	}, [onKey, onCancel]);
	const cost = costSentence(t as never, opts.costUsd);
	return (
		<Dialog
			open
			onOpenChange={(open) => !open && onCancel()}
			role="alertdialog"
			size="sm"
			hideClose
			closeLabel={t("confirmDialog.cancel")}
			initialFocus={okRef}
			title={opts.title ?? t("confirmDialog.title")}
			testId="confirm-dialog"
			description={
				<div className="space-y-2">
					<p className="break-words whitespace-pre-wrap">{message}</p>
					{cost && (
						<p className="text-foreground flex items-start gap-2 font-medium" data-testid="confirm-dialog-cost">
							<CircleDollarSign className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
							<span>
								{cost}
								{opts.costLine ? ` ${opts.costLine}` : ""}
							</span>
						</p>
					)}
				</div>
			}
			footer={
				<>
					<Button type="button" variant="outline" onClick={onCancel} dataTestId="confirm-dialog-cancel">
						{opts.cancelLabel ?? t("confirmDialog.cancel")}
					</Button>
					<Button
						ref={okRef}
						type="button"
						variant={opts.destructive ? "destructive" : "default"}
						onClick={onConfirm}
						dataTestId="confirm-dialog-ok"
					>
						{opts.confirmLabel ?? t("confirmDialog.ok")}
					</Button>
				</>
			}
		/>
	);
}