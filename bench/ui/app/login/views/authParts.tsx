import { Button } from "@/components/ui/button";
import { githubStartUrl } from "@/lib/store/apis/platformApi";
import "@/lib/types/u19I18n";
import { Github, Gavel } from "lucide-react";
import { useTranslation } from "react-i18next";

/** The big "Continue with GitHub" button that leads the sign-in and sign-up cards. */
export function GithubPrimary({ returnTo }: { returnTo: string }) {
	const { t } = useTranslation("platform");
	return (
		<Button asChild size="lg" className="h-11 w-full gap-2.5 text-[15px] font-medium shadow-sm">
			<a href={githubStartUrl(returnTo)} data-testid="auth-github">
				<Github className="size-[18px]" aria-hidden="true" />
				{t("auth.github")}
			</a>
		</Button>
	);
}

/** A thin rule with a label in the middle, between GitHub and the email form. */
export function OrDivider({ label }: { label: string }) {
	return (
		<div className="text-muted-foreground flex items-center gap-3 text-xs" aria-hidden="true">
			<span className="bg-border h-px flex-1" />
			{label}
			<span className="bg-border h-px flex-1" />
		</div>
	);
}

/** A quiet note for hackathon judges: where the judge account comes from. Never holds credentials. */
export function JudgeHint() {
	const { t } = useTranslation("u19");
	return (
		<div className="bg-muted/50 flex items-start gap-3 rounded-xl px-3.5 py-3 text-sm" data-testid="login-judge-hint">
			<span className="bg-background text-primary flex size-7 shrink-0 items-center justify-center rounded-lg border">
				<Gavel className="size-3.5" aria-hidden="true" />
			</span>
			<p className="text-muted-foreground leading-snug">
				<span className="text-foreground font-medium">{t("login.judgeTitle")}</span> {t("login.judgeBody")}
			</p>
		</div>
	);
}
