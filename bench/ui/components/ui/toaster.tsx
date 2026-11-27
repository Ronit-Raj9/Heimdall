import { dismissToast, getUnseenDone, onUnseenDone, toastQueue, type Toast } from "@/lib/notify";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { CircleCheck, Info, X } from "lucide-react";
import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";

/**
 * The toasts of lib/notify.ts, mounted once in the app shell: bottom right (full width on phones),
 * a polite live region, at most 3, 5 s each (8 s with an action or Undo), paused on hover and
 * focus, Escape closes the focused one.
 */
export function Toaster() {
	const toasts = useSyncExternalStore(toastQueue.subscribe, toastQueue.current, toastQueue.current);
	const { t } = useTranslation("toast");
	return (
		<div
			role="status"
			aria-live="polite"
			aria-label={t("region")}
			className="pointer-events-none fixed inset-x-4 bottom-4 z-[110] flex flex-col gap-2 sm:inset-x-auto sm:right-4 sm:w-96"
			data-testid="toaster"
		>
			{toasts.map((toast) => (
				<ToastItem key={toast.id} toast={toast} closeLabel={t("close")} undoLabel={t("undo")} />
			))}
		</div>
	);
}

function ToastItem({ toast, closeLabel, undoLabel }: { toast: Toast; closeLabel: string; undoLabel: string }) {
	const [paused, setPaused] = useState(false);
	const left = useRef(toast.ttl);
	useEffect(() => {
		if (paused) return;
		const start = Date.now();
		const timer = window.setTimeout(() => dismissToast(toast.id), left.current);
		return () => {
			window.clearTimeout(timer);
			left.current -= Date.now() - start;
		};
	}, [paused, toast.id]);
	const Icon = toast.kind === "info" ? Info : CircleCheck;
	const close = () => dismissToast(toast.id);
	const action = toast.action;
	return (
		<div
			className="bg-card text-card-foreground pointer-events-auto flex items-start gap-3 rounded-lg border p-3 text-sm shadow-(--shadow-card) motion-safe:animate-in motion-safe:slide-in-from-bottom-2 motion-safe:fade-in"
			onMouseEnter={() => setPaused(true)}
			onMouseLeave={() => setPaused(false)}
			onFocus={() => setPaused(true)}
			onBlur={(e) => !e.currentTarget.contains(e.relatedTarget as Node | null) && setPaused(false)}
			onKeyDown={(e) => {
				if (e.key === "Escape") {
					e.stopPropagation();
					close();
				}
			}}
			data-testid="toast"
		>
			<Icon className={cn("mt-0.5 size-4 shrink-0", toast.kind === "info" ? "text-muted-foreground" : "text-[var(--success,currentColor)]")} aria-hidden="true" />
			<p className="min-w-0 flex-1 break-words">{toast.text}</p>
			{toast.undo && (
				<button
					type="button"
					className="text-primary shrink-0 font-medium hover:underline"
					onClick={() => void toast.undo?.().finally(close)}
					data-testid="toast-undo"
				>
					{undoLabel}
				</button>
			)}
			{action &&
				("to" in action ? (
					<Link to={action.to as never} className="text-primary shrink-0 font-medium hover:underline" onClick={close} data-testid="toast-action">
						{action.label}
					</Link>
				) : (
					<button
						type="button"
						className="text-primary shrink-0 font-medium hover:underline"
						onClick={() => {
							action.onClick();
							close();
						}}
						data-testid="toast-action"
					>
						{action.label}
					</button>
				))}
			<button
				type="button"
				onClick={close}
				aria-label={closeLabel}
				className="text-muted-foreground hover:text-foreground -m-1 shrink-0 rounded p-1"
				data-testid="toast-close"
			>
				<X className="size-3.5" aria-hidden="true" />
			</button>
		</div>
	);
}

/**
 * How many background jobs finished since the person last opened the Jobs drawer. The shell
 * writes `document.title = titleWithDone(pageTitle(pathname), done)` (lib/notify.ts).
 */
export function useUnseenDone(): number {
	return useSyncExternalStore(
		(cb) => onUnseenDone(() => cb()),
		getUnseenDone,
		getUnseenDone,
	);
}
