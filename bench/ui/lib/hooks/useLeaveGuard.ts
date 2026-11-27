import { useConfirm } from "@/components/platform/confirmDialog";
import "@/components/runs/screenStatesI18n";
import { useBlocker } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

/**
 * Asks before leaving a form with changes (X-97): the browser's own prompt on reload or close,
 * and the shared confirm on an in-app link or Back ("Leave this page? What you typed is kept as a
 * draft."). Pair it with useFormDraft so leaving never loses the text. A form that keeps no draft
 * passes its own body (M8-44), for example "What you changed is not saved.".
 */
export interface LeaveGuardOptions {
	/** The dialog sentence; the default says the text is kept as a draft. */
	body?: string;
}

export function useLeaveGuard(dirty: boolean, opts?: LeaveGuardOptions): void {
	const confirm = useConfirm();
	const { t } = useTranslation("ufs2");
	useBlocker({
		shouldBlockFn: async () => {
			if (!dirty) return false;
			const leave = await confirm(opts?.body ?? t("leave.body"), {
				title: t("leave.title"),
				confirmLabel: t("leave.ok"),
				cancelLabel: t("leave.stay"),
			});
			return !leave;
		},
		enableBeforeUnload: () => dirty,
		disabled: !dirty,
	});
}