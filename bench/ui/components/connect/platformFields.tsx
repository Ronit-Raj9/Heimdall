import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import type { AgentPlatform } from "@/lib/types/agentImport";
import { useTranslation } from "react-i18next";

export interface PlatformFieldsProps {
	platform: AgentPlatform;
	agentId: string;
	onAgentId(v: string): void;
	inputs: Record<string, string>;
	onInputs(v: Record<string, string>): void;
	/** Secret names by role (never values). */
	secrets: Record<string, string>;
	onSecrets(v: Record<string, string>): void;
	/** The org's secret names, offered as picks for every secret field. */
	secretNames: string[];
	/** Id of the secret names list; unique per page. */
	listId: string;
}

/**
 * The fields of one agent platform (shared by Import from a platform and the Connect page): the help
 * and docs link, the platform's agent id, its extra inputs and its secrets as org secret names. The
 * fields are grid children of a two-column form.
 */
export function PlatformFields({
	platform: p,
	agentId,
	onAgentId,
	inputs,
	onInputs,
	secrets,
	onSecrets,
	secretNames,
	listId,
}: PlatformFieldsProps) {
	const { t } = useTranslation("agentImport");
	return (
		<>
			<div className="text-muted-foreground flex flex-col gap-1 text-xs md:col-span-2">
				<span className="flex items-center gap-2">
					<Badge variant="neutral">{t(`kind.${p.kind}`)}</Badge>
					<span>{p.help}</span>
				</span>
				<span>
					{t("imports", { list: p.imports.join(", ") })}.{" "}
					<a className="underline" href={p.docs} target="_blank" rel="noreferrer">
						{t("docs")}
					</a>
				</span>
			</div>
			<label className="flex flex-col gap-1 text-sm md:col-span-2">
				<span className="font-medium">{p.id_label}</span>
				<Input value={agentId} placeholder={p.id_placeholder} onChange={(e) => onAgentId(e.target.value)} data-testid="platform-agent-id" />
			</label>
			{(p.inputs ?? []).map((i) => (
				<label key={i.name} className="flex flex-col gap-1 text-sm">
					<span className="font-medium">
						{i.label}
						{i.required && <span aria-hidden="true"> *</span>}
					</span>
					<Input
						value={inputs[i.name] ?? ""}
						placeholder={i.placeholder}
						onChange={(e) => onInputs({ ...inputs, [i.name]: e.target.value })}
						data-testid={`platform-input-${i.name}`}
					/>
					{i.help && <span className="text-muted-foreground text-xs">{i.help}</span>}
				</label>
			))}
			{p.secrets.map((s) => (
				<label key={s.role} className="flex flex-col gap-1 text-sm">
					<span className="font-medium">
						{s.label}
						{s.required && <span aria-hidden="true"> *</span>}
					</span>
					<Input
						value={secrets[s.role] ?? ""}
						placeholder={s.suggested}
						list={listId}
						onChange={(e) => onSecrets({ ...secrets, [s.role]: e.target.value.toUpperCase() })}
						data-testid={`platform-secret-${s.role}`}
					/>
					<span className="text-muted-foreground text-xs">{s.help ?? t("secretHelp")}</span>
				</label>
			))}
			<datalist id={listId}>
				{secretNames.map((n) => (
					<option key={n} value={n} />
				))}
			</datalist>
		</>
	);
}

/** Secret names a platform starts with: the suggested name of each required secret. */
export function suggestedPlatformSecrets(p: AgentPlatform): Record<string, string> {
	return Object.fromEntries(p.secrets.filter((s) => s.required).map((s) => [s.role, s.suggested]));
}