import { SiteHeader } from "@/components/site/publicFrame";
import { slugify } from "@/components/docs/markdown";
import "@/lib/types/siteI18n";
import { FileWarning } from "lucide-react";
import { useTranslation } from "react-i18next";

/** One numbered section of a legal page. */
export interface LegalSection {
	title: string;
	body: string[];
}

/** The sections of a legal page from the site catalog (terms.sections, privacy.sections). */
export function useLegalSections(key: "terms.sections" | "privacy.sections"): LegalSection[] {
	const { t } = useTranslation("site");
	const raw = t(key, { returnObjects: true }) as unknown;
	return Array.isArray(raw) ? (raw as LegalSection[]) : [];
}

/** The banner every legal page carries until counsel signs it off. */
export function DraftNotice() {
	const { t } = useTranslation("site");
	return (
		<div
			role="note"
			className="border-chart-warning/40 bg-chart-warning/10 text-chart-warning-ink flex items-start gap-3 rounded-xl border px-4 py-3.5 text-sm"
			data-testid="legal-draft"
		>
			<FileWarning className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<span className="flex flex-col gap-1">
				<span className="font-semibold">{t("common.draft")}</span>
				<span>{t("common.draftBody")}</span>
			</span>
		</div>
	);
}

/**
 * A legal page (lane Y08, audit G4-19): title, last update, the draft banner, a contents list and
 * the numbered sections. children render after the sections (the subprocessors table).
 */
export function LegalDocument({ title, updated, sections, children, testId }: { title: string; updated: string; sections: LegalSection[]; children?: React.ReactNode; testId: string }) {
	const { t } = useTranslation("site");
	return (
		<article className="flex min-w-0 flex-col gap-8" data-testid={testId}>
			<SiteHeader title={title}>
				<p className="text-muted-foreground font-mono text-xs">{t("common.updated", { date: updated })}</p>
			</SiteHeader>
			<DraftNotice />
			<div className="grid gap-10 lg:grid-cols-[14rem_minmax(0,1fr)]">
				<nav aria-label={t("common.onThisPage")} className="hidden lg:block">
					<ul className="sticky top-20 flex flex-col gap-1.5 border-l text-[13px]">
						{sections.map((s) => (
							<li key={s.title}>
								<a href={`#${slugify(s.title)}`} className="text-muted-foreground hover:text-foreground -ml-px block border-l border-transparent py-0.5 pl-3 hover:border-foreground/40">
									{s.title}
								</a>
							</li>
						))}
					</ul>
				</nav>
				<div className="flex max-w-3xl min-w-0 flex-col gap-8">
					{sections.map((s) => (
						<section key={s.title} id={slugify(s.title)} className="flex scroll-mt-20 flex-col gap-3">
							<h2 className="text-lg font-semibold">{s.title}</h2>
							{s.body.map((p) => (
								<p key={p.slice(0, 40)} className="text-muted-foreground leading-relaxed">
									{p}
								</p>
							))}
						</section>
					))}
					{children}
				</div>
			</div>
		</article>
	);
}
