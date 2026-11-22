import { HeimdallMark } from "@/components/heimdallMark";
import { ThemeToggle } from "@/components/themeToggle";
import "@/lib/types/statusI18n";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** The frame of the public status page (lane Z14): brand, theme switch and a link into the app. No navigation. */
export function StatusFrame({ children }: { children: ReactNode }) {
	const { t } = useTranslation("status");
	return (
		<div className="bg-background min-h-dvh" data-testid="status-frame">
			<header className="border-b">
				<div className="mx-auto flex max-w-3xl items-center justify-between gap-4 px-4 py-3 md:px-8">
					<span className="flex items-center gap-2 text-sm font-semibold">
						<HeimdallMark className="size-5" />
						Heimdall
					</span>
					<span className="flex items-center gap-3">
						<a href="/" className="text-muted-foreground hover:text-foreground text-xs">
							{t("page.backToApp")}
						</a>
						<ThemeToggle />
					</span>
				</div>
			</header>
			<main id="bench-main" className="mx-auto flex max-w-3xl flex-col gap-6 px-4 py-8 md:px-8">
				{children}
			</main>
		</div>
	);
}
