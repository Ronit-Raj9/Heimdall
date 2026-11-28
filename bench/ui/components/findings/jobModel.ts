import type { PickedModel } from "@/components/models/groupedModelPicker";
import { keyUsable } from "@/components/models/providerCatalog";
import type { ModelsCatalogResponse } from "@/lib/types/models";
import i18n from "@/lib/i18n";
import "./uff1I18n";

/**
 * The model a paid model job (Find out why, Draft with a model) opens on: the organization's
 * default model when one of its keys for that provider works, else the first model of the first
 * working key, else none. Pure.
 */
export function defaultJobModel(data: Pick<ModelsCatalogResponse, "default" | "keys"> | undefined): PickedModel | null {
	if (!data) return null;
	const working = (data.keys ?? []).filter(keyUsable);
	const def = data.default;
	if (def?.model && working.some((k) => k.provider === def.provider)) return { provider: def.provider, model: def.model };
	for (const k of working) {
		const model = k.models?.[0] ?? k.models_available?.[0];
		if (model) return { provider: k.provider, model };
	}
	return null;
}

const prefixes = [/^create job:\s*/i, /^invalid job parameters:\s*/i];

/**
 * A job error as the page shows it: the server's own sentence without the "create job:" and
 * "invalid job parameters:" prefixes, and a plain sentence instead of the gateway's key error.
 */
export function plainJobError(message: string): string {
	let m = message.trim();
	for (let again = true; again; ) {
		again = false;
		for (const p of prefixes) {
			if (p.test(m)) {
				m = m.replace(p, "");
				again = true;
			}
		}
	}
	if (/mint (the job's model )?key|run id does not match/i.test(m)) return i18n.t("jobError.key", { ns: "uff1" });
	return m ? m.charAt(0).toUpperCase() + m.slice(1) : i18n.t("jobError.unknown", { ns: "uff1" });
}
