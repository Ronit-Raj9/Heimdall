import { Button } from "@/components/ui/button";
import "@/app/replays/views/w447I18n";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

/** The Why agents fail header link to shadow replay (/replays), which has no menu item of its own. */
export function ShadowReplayLink() {
	const { t } = useTranslation("w447");
	return (
		<Button variant="outline" size="sm" asChild>
			<Link to={"/replays" as never} data-testid="why-replays-link">
				{t("nav.replays")}
			</Link>
		</Button>
	);
}