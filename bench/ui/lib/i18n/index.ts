import i18n from "i18next";
import { initReactI18next } from "react-i18next";

import { NAMESPACES, resources } from "./resources";

export const DEFAULT_LOCALE = "en";

if (!i18n.isInitialized) {
	void i18n.use(initReactI18next).init({
		resources,
		ns: [...NAMESPACES],
		defaultNS: "bench",
		// Namespaces are chosen via useTranslation(ns), never a "ns:key" prefix. Matches ui/.
		nsSeparator: false,
		lng: DEFAULT_LOCALE,
		fallbackLng: DEFAULT_LOCALE,
		// English is bundled, so init synchronously: the first render and tests see real strings.
		initAsync: false,
		interpolation: {
			escapeValue: false,
		},
		react: {
			useSuspense: false,
		},
	});
}

/** The active UI language, for Intl formatters. Never hardcode "en-US". */
export function getLocale(): string {
	return i18n.resolvedLanguage ?? i18n.language ?? DEFAULT_LOCALE;
}

export { NAMESPACES };
export type { I18nNamespace } from "./resources";
export default i18n;