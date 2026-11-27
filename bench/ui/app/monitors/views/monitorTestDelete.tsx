import "@/components/ui/ufd5I18n";
import { useConfirm, type ConfirmFn, type DestructiveConfirmOptions } from "@/components/platform/confirmDialog";
import { Button } from "@/components/ui/button";
import { notify } from "@/lib/notify";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { archiveApi, MONITOR_DELETE_ROLE } from "@/lib/store/apis/archiveApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { Trash2 } from "lucide-react";
import { useContext, useState } from "react";
import { ReactReduxContext } from "react-redux";
import { useTranslation } from "react-i18next";

/**
 * Asks with the destructive confirm that names the test, then deletes. Returns "deleted",
 * "cancelled" or the server's refusal text.
 */
export async function deleteMonitorTestFlow(
	confirm: ConfirmFn,
	remove: () => Promise<unknown>,
	texts: { question: string; opts: DestructiveConfirmOptions },
): Promise<"deleted" | "cancelled" | { error: string }> {
	if (!(await confirm(texts.question, texts.opts))) return "cancelled";
	try {
		await remove();
	} catch (e) {
		return { error: getBenchErrorMessage(e) || "" };
	}
	return "deleted";
}

/** Delete for one safety rule test (monitor certification), admins only (phase D decision 6). */
export function MonitorTestDelete({ certId, name, onDeleted }: { certId: string; name: string; onDeleted(): void }) {
	const { t } = useTranslation("ufd5");
	const session = useSession();
	const confirm = useConfirm();
	const store = useContext(ReactReduxContext)?.store;
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	if (!store || !canUse(session, MONITOR_DELETE_ROLE)) return null;
	const dispatch = store.dispatch as (a: unknown) => { unwrap(): Promise<unknown> };
	const run = async () => {
		setError(null);
		setBusy(true);
		const out = await deleteMonitorTestFlow(confirm, () => dispatch(archiveApi.endpoints.deleteMonitorTest.initiate(certId)).unwrap(), {
			question: t("monitorDelete.body"),
			opts: { destructive: true, title: t("monitorDelete.title", { name }), confirmLabel: t("monitorDelete.confirm"), cancelLabel: t("monitorDelete.cancel") },
		});
		setBusy(false);
		if (out === "deleted") {
			notify({ text: t("monitorDelete.done", { name }) });
			onDeleted();
		} else if (out !== "cancelled") setError(out.error || t("monitorDelete.failed", { name }));
	};
	return (
		<div className="flex flex-col items-end gap-1">
			<Button size="sm" variant="ghost" isLoading={busy} onClick={() => void run()} dataTestId="monitor-test-delete">
				<Trash2 aria-hidden="true" />
				{t("monitorDelete.action")}
			</Button>
			{error && (
				<p role="alert" className="text-danger-ink max-w-xs text-right text-xs" data-testid="monitor-test-delete-error">
					{error}
				</p>
			)}
		</div>
	);
}
