import { LegalDocument, useLegalSections } from "@/components/site/legalDocument";
import "@/lib/types/siteI18n";
import { useTranslation } from "react-i18next";

interface Subprocessor {
	name: string;
	purpose: string;
	data: string;
}

/**
 * /privacy: the Privacy Policy, a draft under legal review (lane Y08, audit G4-19): what is
 * stored, why, retention, deletion, prompt logs, own keys, and the subprocessors.
 */
export default function PrivacyView() {
	const { t } = useTranslation("site");
	const raw = t("privacy.subprocessors.rows", { returnObjects: true }) as unknown;
	const rows = Array.isArray(raw) ? (raw as Subprocessor[]) : [];
	return (
		<LegalDocument title={t("privacy.title")} updated={t("privacy.updated")} sections={useLegalSections("privacy.sections")} testId="privacy-view">
			<section id="subprocessors" className="flex scroll-mt-20 flex-col gap-3" data-testid="privacy-subprocessors">
				<h2 className="text-lg font-semibold">{t("privacy.subprocessors.title")}</h2>
				<p className="text-muted-foreground leading-relaxed">{t("privacy.subprocessors.intro")}</p>
				<div className="overflow-x-auto rounded-xl border">
					<table className="w-full min-w-[36rem] text-left text-sm">
						<thead className="bg-muted/50 text-muted-foreground text-xs">
							<tr>
								<th scope="col" className="px-4 py-2.5 font-medium">{t("privacy.subprocessors.name")}</th>
								<th scope="col" className="px-4 py-2.5 font-medium">{t("privacy.subprocessors.purpose")}</th>
								<th scope="col" className="px-4 py-2.5 font-medium">{t("privacy.subprocessors.data")}</th>
							</tr>
						</thead>
						<tbody className="divide-y">
							{rows.map((r) => (
								<tr key={r.name}>
									<td className="px-4 py-3 font-medium">{r.name}</td>
									<td className="text-muted-foreground px-4 py-3">{r.purpose}</td>
									<td className="text-muted-foreground px-4 py-3">{r.data}</td>
								</tr>
							))}
						</tbody>
					</table>
				</div>
			</section>
		</LegalDocument>
	);
}
