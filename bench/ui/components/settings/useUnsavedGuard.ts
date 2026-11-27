import { useConfirm } from "@/components/platform/confirmDialog";
import { useBlocker } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import "./uf8aI18n";

/**
 * Warns before unsaved Settings changes are lost: an in-app link asks in the confirm dialog, and
 * closing or reloading the tab gets the browser's own warning. Does nothing while dirty is false.
 */
export function useUnsavedGuard(dirty: boolean): void {
	const { t } = useTranslation("uf8a");
	const confirm = useConfirm();
	useBlocker({
		shouldBlockFn: async () => {
			if (!dirty) return false;
			const leave = await confirm(t("unsaved.body"), {
				title: t("unsaved.title"),
				confirmLabel: t("unsaved.leave"),
				cancelLabel: t("unsaved.stay"),
			});
			return !leave;
		},
		enableBeforeUnload: () => dirty,
		disabled: !dirty,
	});
}