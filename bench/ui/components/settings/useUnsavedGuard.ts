import { useLeaveGuard } from "@/lib/hooks/useLeaveGuard";
import { useTranslation } from "react-i18next";
import "./uf8aI18n";

/**
 * Warns before unsaved Settings changes are lost, through the shared leave guard with the Settings
 * body text (M8-44). Kept as a thin wrapper until its callers call useLeaveGuard directly.
 */
export function useUnsavedGuard(dirty: boolean): void {
	const { t } = useTranslation("uf8a");
	useLeaveGuard(dirty, { body: t("unsaved.body") });
}
