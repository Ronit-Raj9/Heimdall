import { useJobKindLabel } from "@/components/platform/jobsDrawer";
import { useTranslation } from "react-i18next";

/** A job kind in words: the jobs catalog (CLI kinds), then the platform catalog, else the kind as written. */
export function useJobKindName(): (kind: string) => string {
	const { t, i18n } = useTranslation("jobs");
	const platformLabel = useJobKindLabel();
	return (kind) => {
		const key = `kinds.${kind.replace(/\./g, "_")}`;
		return i18n.exists(key, { ns: "jobs" }) ? t(key as "kinds.import_run") : platformLabel(kind);
	};
}
