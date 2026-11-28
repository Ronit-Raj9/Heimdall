import { InlineError } from "@/components/runs/screenStates";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useGetGatewayPromptVersionsQuery, useGetGatewayPromptsQuery, type GwPromptVersion } from "@/lib/store/apis/gatewayPromptsApi";
import { useTranslation } from "react-i18next";
import "./ufl1I18n";

/** True when the server answered 409: no own Bifrost is connected. Pure. */
export function needsConnect(error: unknown): boolean {
	return (error as { status?: unknown } | undefined)?.status === 409;
}

/** The label of one prompt version: "v2 · commit message". Pure. */
export function versionLabel(v: GwPromptVersion): string {
	const msg = v.commit_message.trim();
	return msg ? `v${v.version} · ${msg}` : `v${v.version}`;
}

/** The prompt picker of the prompt version test: the prompts of the org's Bifrost. */
export function PromptPick({ id, value, onChange }: { id: string; value: string; onChange(v: string): void }) {
	const { t } = useTranslation("ufl1");
	const q = useGetGatewayPromptsQuery();
	const connect = needsConnect(q.error);
	const prompts = q.data?.prompts ?? [];
	return (
		<>
			<Select value={value || undefined} onValueChange={onChange} disabled={q.isLoading || !!q.error || prompts.length === 0}>
				<SelectTrigger id={id} className="w-full" data-testid="feature-test-prompt-pick">
					<SelectValue placeholder={q.isLoading ? t("prompt.loading") : t("prompt.pick")} />
				</SelectTrigger>
				<SelectContent>
					{prompts.map((p) => (
						<SelectItem key={p.id} value={p.id}>
							{p.name || p.id}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
			{connect && <p className="text-muted-foreground text-xs" data-testid="feature-test-prompt-connect">{t("prompt.connect")}</p>}
			{q.error && !connect && <InlineError error={q.error} onRetry={() => void q.refetch()} testId="feature-test-prompt-error" />}
			{q.data && prompts.length === 0 && <p className="text-muted-foreground text-xs">{t("prompt.none")}</p>}
		</>
	);
}

/** A version picker of one prompt; "none" is offered for the control ("Compare against"). */
export function PromptVersionPick({
	id,
	promptId,
	value,
	onChange,
	allowNone,
}: {
	id: string;
	promptId: string;
	value: string;
	onChange(v: string): void;
	allowNone?: boolean;
}) {
	const { t } = useTranslation("ufl1");
	const q = useGetGatewayPromptVersionsQuery(promptId, { skip: !promptId });
	const versions = q.data?.versions ?? [];
	return (
		<>
			<Select value={value || undefined} onValueChange={(v) => onChange(v === "none" ? "" : v)} disabled={!promptId || q.isFetching || !!q.error}>
				<SelectTrigger id={id} className="w-full" data-testid={`${id}-pick`}>
					<SelectValue placeholder={!promptId ? t("prompt.pickFirst") : q.isFetching ? t("prompt.loading") : t("prompt.pickVersion")} />
				</SelectTrigger>
				<SelectContent>
					{allowNone && <SelectItem value="none">{t("prompt.noPrompt")}</SelectItem>}
					{versions.map((v) => (
						<SelectItem key={v.version} value={String(v.version)}>
							{versionLabel(v)}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
			{q.error && <InlineError error={q.error} onRetry={() => void q.refetch()} testId={`${id}-error`} />}
			{q.data && versions.length === 0 && <p className="text-muted-foreground text-xs">{t("prompt.noVersions")}</p>}
		</>
	);
}
