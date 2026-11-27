import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/platform/confirmDialog";
import { notify } from "@/lib/notify";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { ARCHIVE_ROLE, archiveApi } from "@/lib/store/apis/archiveApi";
import "@/components/experiments/ufrcI18n";
import { Archive } from "lucide-react";
import { useContext, useState, type ReactNode } from "react";
import { ReactReduxContext } from "react-redux";
import { useTranslation } from "react-i18next";

/** The selection after a click on one row's box: added, or removed when it was in. Pure. */
export function toggled(selected: ReadonlySet<string>, id: string): Set<string> {
	const next = new Set(selected);
	if (next.has(id)) next.delete(id);
	else next.add(id);
	return next;
}

/**
 * Archive many comparisons at once (Q-169, decision 6): row boxes and one Archive button for the
 * picked rows. Archive hides, nothing is deleted; it asks first (the shared confirm opens on
 * Cancel), archives one by one and says how many went, with the refusals. Shown only to the role
 * the server allows; without a store (a static render) there is nothing to select.
 */
export function useBulkArchive(): {
	enabled: boolean;
	selected: ReadonlySet<string>;
	box: (id: string, name: string) => ReactNode;
	bar: ReactNode;
} {
	const { t } = useTranslation("ufrc");
	const session = useSession();
	const confirm = useConfirm();
	const store = useContext(ReactReduxContext)?.store;
	const [selected, setSelected] = useState<Set<string>>(new Set());
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const enabled = Boolean(store) && canUse(session, ARCHIVE_ROLE.experiment);
	const run = async () => {
		if (!store || selected.size === 0) return;
		const ok = await confirm(t("bulk.body", { count: selected.size }), {
			title: t("bulk.title", { count: selected.size }),
			confirmLabel: t("bulk.confirm", { count: selected.size }),
			cancelLabel: t("bulk.cancel"),
		});
		if (!ok) return;
		setBusy(true);
		setError(null);
		const dispatch = store.dispatch as (a: unknown) => { unwrap(): Promise<unknown> };
		let done = 0;
		const failed: string[] = [];
		for (const id of selected) {
			try {
				await dispatch(archiveApi.endpoints.archiveObject.initiate({ kind: "experiment", id })).unwrap();
				done += 1;
			} catch {
				failed.push(id);
			}
		}
		setBusy(false);
		setSelected(new Set(failed));
		if (done > 0) notify({ text: t("bulk.done", { count: done }) });
		if (failed.length > 0) setError(t("bulk.failed", { count: failed.length }));
	};
	const box = (id: string, name: string) =>
		enabled ? (
			<input
				type="checkbox"
				className="accent-primary relative z-10 size-4 shrink-0 cursor-pointer"
				checked={selected.has(id)}
				onChange={() => setSelected((s) => toggled(s, id))}
				aria-label={t("bulk.select", { name })}
				data-testid={`experiment-row-${id}-select`}
			/>
		) : null;
	const bar =
		enabled && (selected.size > 0 || error) ? (
			<div className="flex flex-wrap items-center gap-3 text-sm" data-testid="experiment-bulk-bar">
				{selected.size > 0 && (
					<>
						<span className="tabular-nums">{t("bulk.picked", { count: selected.size })}</span>
						<Button size="sm" variant="outline" isLoading={busy} onClick={() => void run()} dataTestId="experiment-bulk-archive">
							<Archive aria-hidden="true" />
							{t("bulk.action")}
						</Button>
						<Button size="sm" variant="ghost" onClick={() => setSelected(new Set())} dataTestId="experiment-bulk-clear">
							{t("bulk.clear")}
						</Button>
					</>
				)}
				{error && (
					<span role="alert" className="text-danger-ink text-xs">
						{error}
					</span>
				)}
			</div>
		) : null;
	return { enabled, selected, box, bar };
}