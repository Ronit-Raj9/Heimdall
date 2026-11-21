import { HeimdallMark } from "@/components/heimdallMark";
import { ThemeToggle } from "@/components/themeToggle";
import "@/lib/types/shareI18n";
import { Lock } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** The page frame of a shared item: brand, read-only note, theme switch. No navigation. */
export function SharedFrame({ children }: { children: ReactNode }) {
	const { t } = useTranslation("share");
	return (
		<div className="bg-background min-h-dvh" data-testid="shared-frame">
			<header className="border-b">
				<div className="mx-auto flex max-w-5xl items-center justify-between gap-4 px-4 py-3 md:px-8">
					<span className="flex items-center gap-2 text-sm font-semibold">
						<HeimdallMark className="size-5" />
						Heimdall
					</span>
					<span className="flex items-center gap-3">
						<span className="text-muted-foreground hidden items-center gap-1.5 text-xs sm:flex">
							<Lock className="size-3.5" aria-hidden="true" />
							{t("page.readOnly")}
						</span>
						<ThemeToggle />
					</span>
				</div>
			</header>
			<main id="bench-main" className="mx-auto flex max-w-5xl flex-col gap-6 px-4 py-8 md:px-8">
				{children}
			</main>
			<footer className="text-muted-foreground mx-auto max-w-5xl px-4 pb-10 text-xs md:px-8">{t("page.poweredBy")}</footer>
		</div>
	);
}