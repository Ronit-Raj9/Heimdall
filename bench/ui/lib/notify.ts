// Toasts (Spec 5): a module-level queue any screen can push to, shown by the one <Toaster /> in
// the app shell. No toast package; the same listener pattern as lib/billing/planLimit.ts.
// Use a toast only when the result is not visible where the person is (delete, stop, a dialog
// that closes, a start from a list). Past tense with the object name, no "successfully".
// Errors are never toasts: they stay next to the button (InlineError).

export type NotifyAction = { label: string; to: string } | { label: string; onClick(): void };

export interface NotifyInput {
	kind?: "success" | "info";
	text: string;
	action?: NotifyAction;
	/** Offers Undo; the toast closes when it resolves. */
	undo?: () => Promise<unknown>;
}

export interface Toast extends Required<Pick<NotifyInput, "kind" | "text">> {
	id: number;
	action?: NotifyAction;
	undo?: () => Promise<unknown>;
	/** How long it stays, in ms, while not paused. */
	ttl: number;
}

/** At most this many toasts at once; the oldest goes first. */
export const MAX_TOASTS = 3;

type Listener = (toasts: readonly Toast[]) => void;

/** The queue, kept apart from React so tests can drive it in Node. */
export function createToastQueue() {
	let toasts: Toast[] = [];
	let nextId = 1;
	const listeners = new Set<Listener>();
	const emit = () => {
		for (const l of listeners) l(toasts);
	};
	return {
		push(input: NotifyInput): number {
			const id = nextId++;
			const ttl = input.action || input.undo ? 8000 : 5000;
			toasts = [...toasts, { id, kind: input.kind ?? "success", text: input.text, action: input.action, undo: input.undo, ttl }].slice(-MAX_TOASTS);
			emit();
			return id;
		},
		dismiss(id: number): void {
			const next = toasts.filter((t) => t.id !== id);
			if (next.length === toasts.length) return;
			toasts = next;
			emit();
		},
		clear(): void {
			toasts = [];
			emit();
		},
		current: (): readonly Toast[] => toasts,
		subscribe(listener: Listener): () => void {
			listeners.add(listener);
			return () => {
				listeners.delete(listener);
			};
		},
	};
}

/** The app's queue, read by components/ui/toaster.tsx. */
export const toastQueue = createToastQueue();

/** Shows a short notice: notify({ text: t("toast:deleted", { name }) }). Returns its id. */
export function notify(input: NotifyInput): number {
	return toastQueue.push(input);
}

/** Closes one notice early. */
export function dismissToast(id: number): void {
	toastQueue.dismiss(id);
}

// Finished background work not yet seen: the shell prefixes the tab title with "(n done) " until
// the person opens the Jobs drawer.
let unseenDone = 0;
const doneListeners = new Set<(n: number) => void>();

export function addUnseenDone(n = 1): void {
	unseenDone += n;
	for (const l of doneListeners) l(unseenDone);
}

export function clearUnseenDone(): void {
	if (unseenDone === 0) return;
	unseenDone = 0;
	for (const l of doneListeners) l(0);
}

export function getUnseenDone(): number {
	return unseenDone;
}

export function onUnseenDone(listener: (n: number) => void): () => void {
	doneListeners.add(listener);
	return () => {
		doneListeners.delete(listener);
	};
}

/** The tab title with the finished count: titleWithDone("Runs", 2) is "(2 done) Runs". */
export function titleWithDone(title: string, done: number): string {
	return done > 0 ? `(${done} done) ${title}` : title;
}
