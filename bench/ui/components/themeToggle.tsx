import { Button } from "@/components/ui/button";
import { Moon, Sun } from "lucide-react";
import { useTheme } from "next-themes";
import { useTranslation } from "react-i18next";

/** Switches between light and dark. The first visit follows the system theme, as in ui/. */
export function ThemeToggle() {
	const { t } = useTranslation("bench");
	const { resolvedTheme, setTheme } = useTheme();

	return (
		<Button
			variant="ghost"
			size="icon"
			className="text-muted-foreground hover:bg-accent hover:text-accent-foreground relative size-8"
			onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}
			aria-label={t("theme.toggle")}
			data-testid="theme-toggle-btn"
		>
			<Sun className="size-4 scale-100 rotate-0 transition-all dark:scale-0 dark:-rotate-90" strokeWidth={2} />
			<Moon className="absolute size-4 scale-0 rotate-90 transition-all dark:scale-100 dark:rotate-0" strokeWidth={2} />
		</Button>
	);
}