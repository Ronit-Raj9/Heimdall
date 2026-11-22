import { buttonVariants } from "@/components/ui/button";
import { signupOffered, useSession } from "@/lib/session/sessionContext";
import { useGetPublicLibraryQuery, useGetPublicOverviewQuery } from "@/lib/store/apis/publicSiteApi";
import "@/lib/types/siteI18n";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { ArrowRight, Bot, ChartNoAxesColumn, Check, Download, Network, ShieldAlert, Terminal, Zap } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * The landing page (lane Y08, audit G4-03): what Heimdall is, agent first, built on Bifrost, how it
 * works, and the library with the counts this server reports. A hosted visitor at "/" sees it; it
 * reads only /public/overview and /public/benchmarks.
 */
export function LandingView() {
	const { t } = useTranslation("site");
	const session = useSession();
	const overview = useGetPublicOverviewQuery();
	const library = useGetPublicLibraryQuery();
	const offerSignup = signupOffered(session);
	const totals = overview.data?.library;
	const hasLibrary = Boolean(totals && totals.benchmarks > 0);

	const cta = (
		<div className="flex flex-wrap items-center gap-3">
			{offerSignup ? (
				<Link to="/signup" className={buttonVariants({ size: "lg" })} data-testid="landing-signup">
					{t("landing.ctaSignUp")}
					<ArrowRight aria-hidden="true" />
				</Link>
			) : (
				<Link to="/login" className={buttonVariants({ size: "lg" })} data-testid="landing-signin">
					{t("landing.ctaSignIn")}
				</Link>
			)}
			<Link to="/docs" className={buttonVariants({ variant: "outline", size: "lg" })} data-testid="landing-docs">
				{t("landing.ctaDocs")}
			</Link>
		</div>
	);

	const stats = totals
		? [
				{ key: "benchmarks", value: totals.benchmarks },
				{ key: "tasks", value: totals.tasks },
				{ key: "ready", value: totals.ready },
				{ key: "packs", value: totals.packs },
			]
		: [];

	return (
		<div className="flex flex-col gap-24" data-testid="landing">
			<section className="relative flex flex-col gap-8 pt-4 md:pt-10" data-testid="landing-hero">
				<div
					aria-hidden="true"
					className="pointer-events-none absolute -top-24 left-1/2 -z-10 size-[36rem] -translate-x-1/2 rounded-full bg-[oklch(0.55_0.18_272)] opacity-[0.08] blur-3xl"
				/>
				<p className="text-primary font-mono text-xs font-medium tracking-[0.12em] uppercase">{t("landing.eyebrow")}</p>
				<h1 className="max-w-4xl text-[2.6rem] leading-[1.05] font-semibold tracking-[-0.035em] text-balance md:text-6xl">{t("landing.title")}</h1>
				<p className="text-muted-foreground max-w-2xl text-lg leading-relaxed text-pretty">{t("landing.body")}</p>
				{cta}
				{!offerSignup && session.authMode === "hosted" && !session.signedIn && (
					<p className="text-muted-foreground text-sm" data-testid="landing-invite-only">
						{t("landing.inviteOnly")}
					</p>
				)}
				<div className="mt-4 flex flex-col gap-3" data-testid="landing-stats">
					<p className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{t("landing.stats.label")}</p>
					{overview.isLoading ? (
						<div className="bg-muted h-20 animate-pulse rounded-xl" />
					) : hasLibrary ? (
						<dl className="bg-card grid grid-cols-2 divide-x divide-y rounded-xl border sm:grid-cols-4 sm:divide-y-0">
							{stats.map((s) => (
								<div key={s.key} className="flex flex-col gap-1 px-5 py-4" data-testid={`landing-stat-${s.key}`}>
									<dt className="text-muted-foreground order-2 text-sm">{t(`landing.stats.${s.key}` as "landing.stats.tasks")}</dt>
									<dd className="order-1 text-3xl font-semibold tracking-[-0.02em] tabular-nums">{s.value.toLocaleString()}</dd>
								</div>
							))}
						</dl>
					) : (
						<p className="text-muted-foreground bg-card rounded-xl border px-5 py-4 text-sm" data-testid="landing-no-library">
							{t("landing.stats.none")}
						</p>
					)}
				</div>
			</section>

			<section className="grid gap-10 md:grid-cols-[1fr_1.4fr]" data-testid="landing-agent-first">
				<div className="flex flex-col gap-3">
					<h2 className="text-2xl font-semibold tracking-[-0.02em]">{t("landing.agentFirst.title")}</h2>
					<p className="text-muted-foreground leading-relaxed">{t("landing.agentFirst.body")}</p>
				</div>
				<ul className="grid gap-4">
					{(
						[
							{ key: "bring", icon: Bot },
							{ key: "faults", icon: ShieldAlert },
							{ key: "decide", icon: ChartNoAxesColumn },
						] as const
					).map(({ key, icon: Icon }) => (
						<li key={key} className="bg-card flex gap-4 rounded-xl border p-5">
							<span className="bg-primary/10 text-primary flex size-10 shrink-0 items-center justify-center rounded-lg">
								<Icon className="size-5" aria-hidden="true" />
							</span>
							<span className="flex flex-col gap-1">
								<span className="font-semibold">{t(`landing.agentFirst.${key}.title`)}</span>
								<span className="text-muted-foreground text-sm leading-relaxed">{t(`landing.agentFirst.${key}.body`)}</span>
							</span>
						</li>
					))}
				</ul>
			</section>

			<section className="flex flex-col gap-8" data-testid="landing-how">
				<h2 className="text-2xl font-semibold tracking-[-0.02em]">{t("landing.how.title")}</h2>
				<ol className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
					{(["step1", "step2", "step3", "step4"] as const).map((step, i) => (
						<li key={step} className="flex flex-col gap-2 border-t pt-4">
							<span className="text-primary font-mono text-xs font-semibold">0{i + 1}</span>
							<span className="font-semibold">{t(`landing.how.${step}.title`)}</span>
							<span className="text-muted-foreground text-sm leading-relaxed">{t(`landing.how.${step}.body`)}</span>
						</li>
					))}
				</ol>
			</section>

			<section
				className="grid gap-8 overflow-hidden rounded-2xl bg-[oklch(0.2_0.035_272)] p-8 text-[oklch(0.96_0.01_272)] md:grid-cols-2 md:p-12 dark:bg-[oklch(0.17_0.03_272)]"
				data-testid="landing-bifrost"
			>
				<div className="flex flex-col gap-3">
					<span className="flex size-10 items-center justify-center rounded-xl bg-[oklch(1_0_0/0.08)] ring-1 ring-[oklch(1_0_0/0.12)]">
						<Network className="size-5" aria-hidden="true" />
					</span>
					<h2 className="text-2xl font-semibold tracking-[-0.02em]">{t("landing.bifrost.title")}</h2>
					<p className="leading-relaxed text-[oklch(0.82_0.03_272)]">{t("landing.bifrost.body")}</p>
				</div>
				<ul className="flex flex-col justify-center gap-4">
					{(["point1", "point2", "point3"] as const).map((p) => (
						<li key={p} className="flex items-start gap-3 text-sm">
							<Check className="mt-0.5 size-4 shrink-0 text-[oklch(0.8_0.12_160)]" aria-hidden="true" />
							{t(`landing.bifrost.${p}`)}
						</li>
					))}
				</ul>
			</section>

			{(library.data?.packs.length ?? 0) > 0 && (
				<section className="flex flex-col gap-6" data-testid="landing-packs">
					<div className="flex flex-wrap items-end justify-between gap-4">
						<div className="flex flex-col gap-2">
							<h2 className="text-2xl font-semibold tracking-[-0.02em]">{t("landing.libraryTitle")}</h2>
							<p className="text-muted-foreground">{t("landing.libraryBody")}</p>
						</div>
						<Link to="/library" className={buttonVariants({ variant: "outline" })}>
							{t("landing.ctaLibrary")}
							<ArrowRight aria-hidden="true" />
						</Link>
					</div>
					<ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
						{library.data?.packs.map((p) => (
							<li key={p.id}>
								<Link
									to="/library"
									search={{ pack: p.id } as never}
									className="bg-card hover:border-foreground/20 flex h-full flex-col gap-1.5 rounded-xl border p-5 transition-colors"
									data-testid={`landing-pack-${p.id}`}
								>
									<span className="font-semibold">{p.name}</span>
									<span className="text-muted-foreground text-sm">{p.description}</span>
									<span className="text-muted-foreground mt-auto pt-2 font-mono text-xs">
										{t("landing.packBenchmarks", { count: p.benchmarks.length })}
									</span>
								</Link>
							</li>
						))}
					</ul>
				</section>
			)}

			<section className="grid gap-6 rounded-2xl border p-8 md:grid-cols-[1fr_auto] md:items-center md:p-10" data-testid="landing-dev">
				<div className="flex gap-4">
					<span className="bg-muted flex size-10 shrink-0 items-center justify-center rounded-lg">
						<Terminal className="size-5" aria-hidden="true" />
					</span>
					<div className="flex flex-col gap-1">
						<h2 className="text-lg font-semibold">{t("landing.devTitle")}</h2>
						<p className="text-muted-foreground text-sm">{t("landing.devBody")}</p>
					</div>
				</div>
				<Link to="/downloads" className={buttonVariants({ variant: "outline" })}>
					<Download aria-hidden="true" />
					{t("landing.devCta")}
				</Link>
			</section>

			<section className={cn("flex flex-col items-start gap-5 pb-6")} data-testid="landing-final">
				<span className="bg-primary/10 text-primary flex size-10 items-center justify-center rounded-lg">
					<Zap className="size-5" aria-hidden="true" />
				</span>
				<h2 className="text-3xl font-semibold tracking-[-0.025em] text-balance">{t("landing.finalTitle")}</h2>
				<p className="text-muted-foreground">{t("landing.finalBody")}</p>
				{cta}
			</section>
		</div>
	);
}
