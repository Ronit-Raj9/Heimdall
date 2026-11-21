import { createContext, useContext, type ReactNode } from "react";

const EmbeddedContext = createContext(false);

/**
 * Marks a page rendered inside another page's tab (a Settings tab showing the API keys page).
 * The inner page's header steps down a level, so the outer page keeps the one h1.
 */
export function EmbeddedPage({ children }: { children: ReactNode }) {
	return <EmbeddedContext.Provider value={true}>{children}</EmbeddedContext.Provider>;
}

/** True inside EmbeddedPage. */
export function useEmbedded(): boolean {
	return useContext(EmbeddedContext);
}
