import { BenchHeader } from "@/components/benchHeader";
import { BenchSidebar } from "@/components/benchSidebar";
import { ThemeProvider } from "@/components/themeProvider";
import i18n from "@/lib/i18n";
import { ReduxProvider } from "@/lib/store";
import { NuqsAdapter } from "nuqs/adapters/tanstack-router";
import { I18nextProvider } from "react-i18next";

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
	return (
		<div className="bg-background flex min-h-dvh flex-col md:h-dvh md:flex-row">
			<BenchSidebar />
			<div className="flex min-h-0 w-full min-w-0 flex-1 flex-col">
				<BenchHeader />
				<div className="dark:bg-card mx-0 min-h-0 min-w-0 flex-1 overflow-auto border border-gray-200 bg-white md:mr-3 md:mb-3 md:rounded-md dark:border-zinc-800">
					<main className="relative mx-auto flex min-h-0 flex-col px-4 py-4 md:px-14">{children}</main>
				</div>
			</div>
		</div>
	);
}