import { Button } from "@/components/ui/button";
import { githubStartUrl } from "@/lib/store/apis/platformApi";
import "@/lib/types/u19I18n";
import { Github } from "lucide-react";
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
