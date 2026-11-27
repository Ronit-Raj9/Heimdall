import { pageWidthClass } from "@/components/shell/pageWidth";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { SiteHeader } from "@/components/site/publicFrame";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { signupOffered, useSession } from "@/lib/session/sessionContext";
import { useGetPublicPricingQuery } from "@/lib/store/apis/publicSiteApi";
import "@/lib/types/siteI18n";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { Check } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * /pricing (lane Y08, audit G4-03, G4-10): the plans the server publishes (GET /public/pricing),
 * free during beta with your own model key until a plan source replaces it. Every plan states its
 * limits.
 */
export default function PricingView() {
	const { t } = useTranslation("site");
	const session = useSession();
	const pricing = useGetPublicPricingQuery();
	const signupTo = session.signedIn || session.authMode !== "hosted" ? "/" : signupOffered(session) ? "/signup" : "/login";
	return (
		<section className={cn(pageWidthClass("read"), "flex flex-col gap-10")} data-page-width="read" data-testid="pricing-view">
			<SiteHeader title={pricing.data?.headline || t("pricing.title")} subtitle={t("pricing.subtitle")} />
			{pricing.isLoading ? (
				<BlockSkeleton rows={4} testId="pricing-loading" />
			) : pricing.error || !pricing.data ? (
				<LoadError title={t("common.loadError")} error={pricing.error} onRetry={pricing.refetch} testId="pricing-error" />
			) : (
				<>
					{pricing.data.note && <p className="text-muted-foreground max-w-3xl leading-relaxed" data-testid="pricing-note">{pricing.data.note}</p>}
					<ul className={cn("grid gap-5", pricing.data.plans.length > 1 && "md:grid-cols-2 lg:grid-cols-3")} data-testid="pricing-plans">
						{pricing.data.plans.map((p) => (
							<li key={p.id} className="bg-card flex max-w-3xl flex-col gap-6 rounded-2xl border p-7 shadow-(--shadow-card)" data-testid={`pricing-plan-${p.id}`}>
								<div className="flex flex-col gap-2">
									<div className="flex items-center gap-2">
										<h2 className="text-lg font-semibold">{p.name}</h2>
										{pricing.data?.beta && <Badge>{t("pricing.beta")}</Badge>}
									</div>
									<p className="flex items-baseline gap-2">
										<span className="text-4xl font-semibold tracking-[-0.03em]">{p.price}</span>
										<span className="text-muted-foreground text-sm">{p.period}</span>
									</p>
									<p className="text-muted-foreground text-sm">{p.summary}</p>
								</div>
								<ul className="flex flex-col gap-2 text-sm">
									{p.features.map((f) => (
										<li key={f} className="flex items-start gap-2.5">
											<Check className="text-chart-success mt-0.5 size-4 shrink-0" aria-hidden="true" />
											{f}
										</li>
									))}
								</ul>
								{p.limits.length > 0 && (
									<div className="flex flex-col gap-2">
										<p className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{t("pricing.limits")}</p>
										<dl className="divide-y rounded-lg border text-sm" data-testid={`pricing-limits-${p.id}`}>
											{p.limits.map((l) => (
												<div key={l.name} className="grid gap-1 px-3.5 py-2.5 sm:grid-cols-[12rem_minmax(0,1fr)]">
													<dt className="font-medium">{l.name}</dt>
													<dd className="text-muted-foreground">{l.value}</dd>
												</div>
											))}
										</dl>
									</div>
								)}
								<div>
									{p.cta === "contact" ? (
										<Link to="/security" className={buttonVariants({ variant: "outline" })}>
											{t("pricing.ctaContact")}
										</Link>
									) : (
										<Link to={signupTo} className={buttonVariants()} data-testid={`pricing-cta-${p.id}`}>
											{t("pricing.ctaSignUp")}
										</Link>
									)}
								</div>
							</li>
						))}
					</ul>
					{pricing.data.faq.length > 0 && (
						<section className="flex max-w-3xl flex-col gap-4" data-testid="pricing-faq">
							<h2 className="text-xl font-semibold">{t("pricing.faq")}</h2>
							<dl className="flex flex-col gap-5">
								{pricing.data.faq.map((f) => (
									<div key={f.q} className="flex flex-col gap-1">
										<dt className="font-medium">{f.q}</dt>
										<dd className="text-muted-foreground leading-relaxed">{f.a}</dd>
									</div>
								))}
							</dl>
						</section>
					)}
				</>
			)}
		</section>
	);
}
