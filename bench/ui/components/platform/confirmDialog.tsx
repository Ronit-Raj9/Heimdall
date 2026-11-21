import { Button } from "@/components/ui/button";
import { createContext, useCallback, useContext, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** Options for one confirmation: labels default to the platform catalog's confirmDialog keys. */
export interface ConfirmOptions {
	title?: string;
	confirmLabel?: string;
	cancelLabel?: string;
	/** Paints the confirm button red, for deletes and other changes that cannot be undone. */
	destructive?: boolean;
}

export type ConfirmFn = (message: string, opts?: ConfirmOptions) => Promise<boolean>;

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
	const confirm = useCallback<ConfirmFn>((message, opts) => store.ask(message, opts), [store]);
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

/** The modal itself: an alertdialog with focus on Confirm, Escape or the backdrop cancels. */
export function ConfirmDialog({ message, opts = {}, onConfirm, onCancel, onKey }: ConfirmDialogProps) {
	const { t } = useTranslation("platform");
	const titleId = useId();
	const descId = useId();
	const okRef = useRef<HTMLButtonElement>(null);
	useEffect(() => {
		const prev = typeof document !== "undefined" ? (document.activeElement as HTMLElement | null) : null;
		okRef.current?.focus();
		const handle = (e: KeyboardEvent) => {
			const handled = onKey ? onKey(e.key) : e.key === "Escape" && (onCancel(), true);
			if (handled) {
				e.preventDefault();
				e.stopPropagation();
			}
		};
		window.addEventListener("keydown", handle, true);
		return () => {
			window.removeEventListener("keydown", handle, true);
			prev?.focus?.();
		};
	}, [onKey, onCancel]);
	return (
		<div
			className="fixed inset-0 z-[100] flex items-center justify-center bg-black/40 p-4"
			data-testid="confirm-dialog-backdrop"
			onMouseDown={(e) => {
				if (e.target === e.currentTarget) onCancel();
			}}
		>
			<div
				role="alertdialog"
				aria-modal="true"
				aria-labelledby={titleId}
				aria-describedby={descId}
				className="bg-card text-card-foreground w-full max-w-md space-y-4 rounded-lg border p-5 text-left shadow-lg"
				data-testid="confirm-dialog"
			>
				<h2 id={titleId} className="text-sm font-semibold">
					{opts.title ?? t("confirmDialog.title")}
				</h2>
				<p id={descId} className="text-muted-foreground text-sm break-words whitespace-pre-wrap">
					{message}
				</p>
				<div className="flex justify-end gap-2">
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
				</div>
			</div>
		</div>
	);
}
