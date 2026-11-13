import "i18next";

import type { resources } from "./resources";

// Types t() keys against the English catalog, as ui/ does, so an unknown key fails the typecheck.
declare module "i18next" {
	interface CustomTypeOptions {
		defaultNS: "bench";
		nsSeparator: false;
		resources: (typeof resources)["en"];
	}
}