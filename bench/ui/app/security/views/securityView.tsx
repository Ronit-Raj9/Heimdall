import { SiteHeader } from "@/components/site/publicFrame";
import { buttonVariants } from "@/components/ui/button";
import "@/lib/types/siteI18n";
import { Ban, Fingerprint, KeyRound, ScrollText, ShieldCheck } from "lucide-react";
import { useTranslation } from "react-i18next";

const SECTIONS = [
	{ key: "keys", icon: KeyRound },
	{ key: "isolation", icon: ShieldCheck },
	{ key: "access", icon: Fingerprint },
	{ key: "evidence", icon: ScrollText },
	{ key: "notYet", icon: Ban },
] as const;

/**
 * /security (lane Y08, audit G4-19): how keys, isolation, access and evidence are protected, what
 * is not available yet, and where to report a vulnerability (/.well-known/security.txt).
 */
export default function SecurityView() {
	const { t } = useTranslation("site");
	const items = (key: (typeof SECTIONS)[number]["key"]) => {
		const raw = t(`security.sections.${key}.items`, { returnObjects: true }) as unknown;
		return Array.isArray(raw) ? (raw as string[]) : [];
	};
	return (
		<section className="flex min-w-0 flex-col gap-10" data-testid="security-view">
			<SiteHeader title={t("security.title")} subtitle={t("security.subtitle")} />
			<div className="bg-card flex flex-col gap-4 rounded-xl border p-6 md:flex-row md:items-center md:justify-between" data-testid="security-report">
				<div className="flex max-w-2xl flex-col gap-1.5">
					<h2 className="text-lg font-semibold">{t("security.report.title")}</h2>
					<p className="text-muted-foreground text-sm leading-relaxed">{t("security.report.body")}</p>
				</div>
				<a href="/.well-known/security.txt" className={buttonVariants({ variant: "outline" })} data-testid="security-txt">
					{t("security.report.cta")}
				</a>
			</div>
			<div className="grid gap-5 md:grid-cols-2">
				{SECTIONS.map(({ key, icon: Icon }) => (
					<section key={key} className="flex flex-col gap-3 rounded-xl border p-6" data-testid={`security-${key}`}>
						<h2 className="flex items-center gap-2.5 font-semibold">
							<Icon className="text-primary size-4.5" aria-hidden="true" />
							{t(`security.sections.${key}.title`)}
						</h2>
						<ul className="text-muted-foreground flex list-disc flex-col gap-2 pl-5 text-sm leading-relaxed">
							{items(key).map((item) => (
								<li key={item}>{item}</li>
							))}
						</ul>
					</section>
				))}
			</div>
		</section>
	);
}
