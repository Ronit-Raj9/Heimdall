import { BenchHeader } from "@/components/benchHeader";
import { BenchSidebar } from "@/components/benchSidebar";
import { ThemeProvider } from "@/components/themeProvider";
import i18n from "@/lib/i18n";
import { ReduxProvider } from "@/lib/store";
import { NuqsAdapter } from "nuqs/adapters/tanstack-router";
import { I18nextProvider, useTranslation } from "react-i18next";

/**
 * Providers and shell for the standalone bench app. When the screens move into ui/, ui/'s own
 * ClientLayout provides all of this and only the route folders and components move.
 */
export function ClientLayout({ children }: { children: React.ReactNode }) {
	return (
		<I18nextProvider i18n={i18n}>
			<ThemeProvider attribute="class" defaultTheme="system" enableSystem>
				<ReduxProvider>
					<NuqsAdapter>
						<Shell>{children}</Shell>
					</NuqsAdapter>
				</ReduxProvider>
			</ThemeProvider>
		</I18nextProvider>
	);
}

// Same frame as ui/: page background behind the nav and header, content in a bordered card.
function Shell({ children }: { children: React.ReactNode }) {
	const { t } = useTranslation("bench");
	return (
		<div className="bg-background flex min-h-dvh flex-col md:h-dvh md:flex-row">
			<a
				href="#bench-main"
				className="bg-primary text-primary-foreground sr-only z-50 rounded-md px-3 py-2 text-sm focus:not-sr-only focus:fixed focus:top-3 focus:left-3"
			>
				{t("app.skipToContent")}
			</a>
			<BenchSidebar />
			<div className="flex min-h-0 w-full min-w-0 flex-1 flex-col">
				<BenchHeader />
				<div
					className="bg-card mx-0 min-h-0 min-w-0 flex-1 overflow-auto border-y shadow-(--shadow-card) md:mr-3 md:mb-3 md:rounded-xl md:border"
					data-bench-content
				>
					<main
						id="bench-main"
						tabIndex={-1}
						className="relative mx-auto flex min-h-0 w-full max-w-[1600px] flex-col px-4 py-6 md:px-8 lg:px-10 xl:px-12"
						data-bench-main
					>
						{children}
					</main>
				</div>
			</div>
		</div>
	);
}