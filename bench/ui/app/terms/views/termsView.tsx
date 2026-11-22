import { LegalDocument, useLegalSections } from "@/components/site/legalDocument";
import "@/lib/types/siteI18n";
import { useTranslation } from "react-i18next";

/** /terms: the Terms of Service, a draft under legal review (lane Y08, audit G4-19). */
export default function TermsView() {
	const { t } = useTranslation("site");
	return <LegalDocument title={t("terms.title")} updated={t("terms.updated")} sections={useLegalSections("terms.sections")} testId="terms-view" />;
}
