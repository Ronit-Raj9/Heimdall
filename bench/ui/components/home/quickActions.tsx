import { QUICKSTART_STEPS } from "@/components/quickstart/quickstart.utils";
import { Button } from "@/components/ui/button";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { ArrowRight, Bot, FlaskConical, PackagePlus, Rocket, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";

interface Action {
	key: "newExperiment" | "addSuite" | "addAgent" | "getStarted";
	to: "/experiments/new" | "/imports" | "/agents" | "/quickstart";
	icon: LucideIcon;
	/** Needs a member: a viewer or demo visitor cannot start experiments. */
	write?: boolean;
}

// "Add a suite" opens /imports, which becomes the Suites add flow (its redirect lands there).
const ACTIONS: readonly Action[] = [
	{ key: "newExperiment", to: "/experiments/new", icon: FlaskConical, write: true },
	{ key: "addSuite", to: "/imports", icon: PackagePlus },
	{ key: "addAgent", to: "/agents", icon: Bot },
	{ key: "getStarted", to: "/quickstart", icon: Rocket },
];

/** The things to do from Home, each one sentence. The first is the primary one. */
export function QuickActions({ hide = [] }: { hide?: readonly Action["key"][] }) {
	const { t } = useTranslation("bench");
	const session = useSession();
	const actions = ACTIONS.filter((action) => (!action.write || canUse(session, "member")) && !hide.includes(action.key));
	return (
		<ul className="grid grid-cols-2 gap-2.5 xl:grid-cols-4" data-testid="home-actions">
			{actions.map(({ key, to, icon: Icon }, index) => (
				<li key={key} className="min-w-0">
					<Link
						to={to}
						className={cn(
							"group bg-card hover:border-primary/40 hover:bg-accent/40 flex h-full min-w-0 items-center gap-3 rounded-lg border p-3 shadow-(--shadow-card) transition-colors sm:items-start sm:p-3.5",
							"focus-visible:ring-ring/60 focus-visible:ring-2 focus-visible:outline-none",
						)}
						data-testid={`home-action-${key}`}
					>
						<span
							className={cn(
								"flex size-8 shrink-0 items-center justify-center rounded-md",
								index === 0 ? "bg-primary text-primary-foreground" : "bg-muted text-foreground",
							)}
						>
							<Icon className="size-4" aria-hidden="true" />
						</span>
						<span className="flex min-w-0 flex-1 flex-col gap-0.5">
							<span className="text-foreground flex items-center gap-1 text-sm font-medium">
								{t(`home.actions.${key}`)}
								<ArrowRight
									className="size-3.5 -translate-x-1 opacity-0 transition-[opacity,transform] duration-150 group-hover:translate-x-0 group-hover:opacity-100 motion-reduce:transition-none"
									aria-hidden="true"
								/>
							</span>
							<span className="text-muted-foreground hidden text-xs sm:block">{t(`home.actions.${key}Hint`)}</span>
						</span>
					</Link>
				</li>
			))}
		</ul>
	);
}

/** Shown when no experiment exists yet: the four quickstart steps and the way in. */
export function GetStartedCard() {
	const { t } = useTranslation("bench");
	const session = useSession();
	return (
		<section
			className="border-primary/30 bg-primary/[0.04] flex flex-col gap-4 rounded-xl border p-5 md:flex-row md:items-center md:gap-8 md:p-6"
			data-testid="home-get-started"
			aria-labelledby="home-get-started-title"
		>
			<div className="flex min-w-0 flex-1 flex-col gap-2">
				<h2 id="home-get-started-title" className="text-foreground text-lg font-semibold tracking-tight">
					{t("home.getStarted.title")}
				</h2>
				<p className="text-muted-foreground max-w-xl text-sm">{t("home.getStarted.body")}</p>
				<ol className="mt-1 grid gap-1.5 sm:grid-cols-2">
					{QUICKSTART_STEPS.map((step, index) => (
						<li key={step.key} className="flex min-w-0 items-center gap-2 text-sm">
							<span className="bg-primary/12 text-primary flex size-5 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold tabular-nums">
								{index + 1}
							</span>
							<span className="truncate">{t(`quickstart.step.${step.key}.title`)}</span>
						</li>
					))}
				</ol>
			</div>
			<div className="flex shrink-0 flex-wrap gap-2">
				<Button asChild dataTestId="home-get-started-open">
					<Link to="/quickstart">
						<Rocket aria-hidden="true" />
						{t("home.getStarted.open")}
					</Link>
				</Button>
				{canUse(session, "member") && (
					<Button asChild variant="outline" dataTestId="home-get-started-new">
						<Link to="/experiments/new">{t("home.getStarted.newExperiment")}</Link>
					</Button>
				)}
			</div>
		</section>
	);
}