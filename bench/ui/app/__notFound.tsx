import { Button } from "@/components/ui/button";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

export function NotFoundComponent() {
	const { t } = useTranslation("bench");
	return (
		<section className="flex flex-col items-start gap-3" data-testid="not-found-view">
			<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("notFound.title")}</h1>
			<p className="text-muted-foreground text-sm">{t("notFound.description")}</p>
			<Button asChild variant="outline" size="sm">
				<Link to="/verdict" data-testid="not-found-link-verdict">
					{t("notFound.back")}
				</Link>
			</Button>
		</section>
	);
}