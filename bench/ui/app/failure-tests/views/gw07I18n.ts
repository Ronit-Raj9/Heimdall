import type gw07En from "@/locales/en/gw07.json";
import { faultName } from "@/components/reportcard/reportCard.utils";

// Types the "gw07" namespace (locales/en/gw07.json): fix wave 2 lane G07 strings of Failure tests and the benchmark page.
declare module "@/lib/i18n/resources" {
	interface LaneResources {
		gw07: typeof gw07En;
	}
}

/**
 * A fault's name in the same words the form uses: the catalog label (GET /faults) when the catalog
 * has the id, else the report card name with its first letter in capitals ("model-down" reads
 * "Model down", never the raw id).
 */
export function ftFaultName(id: string, catalog?: readonly { id: string; label: string }[]): string {
	const hit = catalog?.find((f) => f.id === id)?.label;
	if (hit) return hit;
	const name = faultName(id);
	return name.charAt(0).toUpperCase() + name.slice(1);
}
