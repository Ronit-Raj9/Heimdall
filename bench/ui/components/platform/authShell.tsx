import { HeimdallMark } from "@/components/heimdallMark";
import { ThemeToggle } from "@/components/themeToggle";
import "@/lib/types/u19I18n";
import { Bot, ShieldCheck, Waypoints } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** The gate drawn large: nested arches behind the brand panel, echoing the mark. Decorative. */
function GateLines() {
	return (
		<svg
			viewBox="0 0 400 400"
			fill="none"
			stroke="currentColor"
			className="pointer-events-none absolute -right-24 -bottom-16 size-[34rem] opacity-[0.09]"
			aria-hidden="true"
			focusable="false"
		>
			{[0, 1, 2, 3, 4, 5].map((i) => {
				const inset = 30 + i * 26;
				const width = 400 - inset * 2;
				return (
					<path
						key={i}
						d={`M${inset} 400V${200 - i * 6}a${width / 2} ${width / 2} 0 0 1 ${width} 0V400`}
						strokeWidth={i === 0 ? 2 : 1.25}
					/>
				);
			})}
			<path d="M150 300c16-21 33-31 50-31s34 10 50 31c-16 21-33 31-50 31s-34-10-50-31Z" strokeWidth={2} />
			<circle cx="200" cy="300" r="7" fill="currentColor" stroke="none" />
		</svg>
	);
}

/**
 * The frame of the sign-in, sign-up, invitation and reset pages: the product pitch on wide screens,
 * the form in a card on the right, the theme switch in the corner. No navigation: there is nothing
 * to reach yet.
 */
export function AuthShell({ children }: { children: ReactNode }) {
	const { t } = useTranslation("u19");
	const { t: tb } = useTranslation("bench");
	const benefits = [
		{ icon: Bot, key: "run" },
		{ icon: Waypoints, key: "bifrost" },
		{ icon: ShieldCheck, key: "sealed" },
	] as const;
	return (
		<div className="bg-background grid min-h-dvh lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]" data-testid="auth-shell">
			<aside className="relative hidden overflow-hidden bg-[oklch(0.2_0.035_272)] text-[oklch(0.96_0.01_272)] lg:flex lg:flex-col lg:justify-between lg:p-12 xl:p-16 dark:bg-[oklch(0.17_0.03_272)]">
				<div
					aria-hidden="true"
					className="pointer-events-none absolute inset-0 [background-image:linear-gradient(to_right,currentColor_1px,transparent_1px),linear-gradient(to_bottom,currentColor_1px,transparent_1px)] [mask-image:radial-gradient(ellipse_at_top_left,black,transparent_70%)] [background-size:32px_32px] opacity-[0.07]"
				/>
				<div
					aria-hidden="true"
					className="pointer-events-none absolute -top-32 -left-24 size-[28rem] rounded-full bg-[oklch(0.55_0.18_272)] opacity-25 blur-3xl"
				/>
				<GateLines />
				<div className="relative flex items-center gap-3">
					<span className="flex size-10 items-center justify-center rounded-xl bg-[oklch(0.96_0.01_272)] text-[oklch(0.2_0.035_272)]">
						<HeimdallMark className="size-6" />
					</span>
					<span className="text-lg font-semibold tracking-[-0.01em]">{tb("app.name")}</span>
				</div>
				<div className="relative flex max-w-lg flex-col gap-8" data-testid="auth-pitch">
					<div className="flex flex-col gap-4">
						<h2 className="text-[2.5rem] leading-[1.08] font-semibold tracking-[-0.03em] text-balance">{t("pitch.title")}</h2>
						<p className="text-base leading-relaxed text-[oklch(0.82_0.03_272)]">{t("pitch.body")}</p>
					</div>
					<ul className="flex flex-col gap-5">
						{benefits.map(({ icon: Icon, key }) => (
							<li key={key} className="flex items-start gap-4" data-testid={`auth-benefit-${key}`}>
								<span className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-[oklch(1_0_0/0.08)] ring-1 ring-[oklch(1_0_0/0.12)]">
									<Icon className="size-5" aria-hidden="true" />
								</span>
								<span className="flex flex-col gap-0.5">
									<span className="text-[15px] font-semibold">{t(`pitch.${key}.title`)}</span>
									<span className="text-sm text-[oklch(0.78_0.03_272)]">{t(`pitch.${key}.body`)}</span>
								</span>
							</li>
						))}
					</ul>
				</div>
				<p className="relative font-mono text-[11px] tracking-[0.08em] text-[oklch(0.7_0.03_272)] uppercase">{t("pitch.footer")}</p>
			</aside>
			<div className="bg-muted/30 relative flex min-h-dvh flex-col">
				<div className="flex items-center justify-between px-5 py-4 lg:justify-end">
					<span className="flex items-center gap-2 lg:hidden">
						<span className="bg-foreground text-background flex size-8 items-center justify-center rounded-lg">
							<HeimdallMark className="size-5" />
						</span>
						<span className="text-[15px] font-semibold">{tb("app.name")}</span>
					</span>
					<ThemeToggle />
				</div>
				<main className="flex flex-1 items-start justify-center px-4 pt-4 pb-16 sm:items-center sm:px-5 sm:pt-0" id="bench-main">
					<div className="animate-in fade-in-0 slide-in-from-bottom-2 flex w-full max-w-[440px] flex-col gap-5 duration-300 ease-(--ease-out)">
						<p className="text-muted-foreground text-center text-sm font-medium lg:hidden">{t("pitch.title")}</p>
						<div className="bg-card rounded-2xl border p-6 shadow-sm sm:p-8" data-testid="auth-card">
							{children}
						</div>
					</div>
				</main>
			</div>
		</div>
	);
}
