import { ReferenceBoardPanel } from "@/components/reference/referenceBoard";
import "@/lib/types/referenceI18n";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";

/** /reference: the public reference board, in the read-only frame (no navigation, no sign-in). */
export default function ReferenceView() {
	const { t } = useTranslation("reference");
	useEffect(() => {
		document.title = `${t("title")} · Heimdall`;
	}, [t]);
	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="reference-page">
			<header className="flex flex-col gap-1">
				<h1 className="text-2xl font-semibold tracking-tight">{t("title")}</h1>
				<p className="text-muted-foreground max-w-3xl text-sm text-pretty">{t("subtitle")}</p>
			</header>
			<ReferenceBoardPanel />
		</section>
	);
}
