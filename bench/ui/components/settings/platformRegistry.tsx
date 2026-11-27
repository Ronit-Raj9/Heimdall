// Fix wave 3 (N216): the image registry block of a sandbox platform's card.
// Modal pulls Heimdall's images from the org's own registry, so a platform
// takes runs only once a registry login is saved here.
import { ConfirmButton } from "@/components/platform/badges";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	type WorkerPlatform,
	useRemovePlatformRegistryMutation,
	useSavePlatformRegistryMutation,
} from "@/lib/store/apis/workerPlatformsApi";
import { CheckCircle2 } from "lucide-react";
import { useContext, useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { ReactReduxContext } from "react-redux";
import { When } from "./settingsBits";

/** Platforms whose card takes an image registry login. */
export const REGISTRY_PLATFORMS = ["modal"];

/** True when the platform's card shows the registry block. */
export function takesRegistry(p: WorkerPlatform): boolean {
	return p.connected && REGISTRY_PLATFORMS.includes(p.name);
}

/** Why "Sign in and save" is disabled, or undefined once every field is filled. */
export function registryHint(registry: string, username: string, token: string): "needAll" | undefined {
	return registry.trim() && username.trim() && token.trim() ? undefined : "needAll";
}

export interface PlatformRegistryFormProps {
	platform: WorkerPlatform;
	busy?: boolean;
	error?: string;
	notice?: string;
	/** Start with the form open even when a registry is saved (tests). */
	editing?: boolean;
	onSave: (registry: string, username: string, token: string) => void;
	onRemove: () => void;
}

/** The registry block: the saved login (never its token) or the form. Pure, so it renders on the server in tests. */
export function PlatformRegistryForm({
	platform: p,
	busy,
	error,
	notice,
	editing: startEditing,
	onSave,
	onRemove,
}: PlatformRegistryFormProps) {
	const { t } = useTranslation("workerPlatforms");
	const formId = useId();
	const saved = p.registry;
	const [editing, setEditing] = useState(Boolean(startEditing) || !saved);
	const [registry, setRegistry] = useState(saved?.registry ?? "");
	const [username, setUsername] = useState(saved?.username ?? "");
	const [token, setToken] = useState("");
	const hint = registryHint(registry, username, token);
	const submit = (event: FormEvent) => {
		event.preventDefault();
		if (hint) return;
		onSave(registry.trim(), username.trim(), token.trim());
		setToken("");
	};
	return (
		<section className="flex flex-col gap-2" data-testid={`platform-${p.name}-registry`}>
			{/* Q-247: the disclosure around this block already carries the "Image registry" title. */}
			{!saved && (
				<p className="text-chart-warning-ink text-xs" data-testid={`platform-${p.name}-registry-needed`}>
					{t("registry.needed")}
				</p>
			)}
			{saved && !editing ? (
				<div className="flex flex-wrap items-center justify-between gap-2">
					<p className="text-muted-foreground min-w-0 text-xs break-all" data-testid={`platform-${p.name}-registry-saved`}>
						<span className="font-mono">{t("registry.savedAs", { registry: saved.registry, username: saved.username })}</span>
						{saved.token_last4 ? <span> · {t("registry.tokenEnding", { last4: saved.token_last4 })}</span> : null}
						{saved.tested_at ? (
							<span>
								{" "}
								· <When value={saved.tested_at} />
							</span>
						) : null}
					</p>
					<div className="flex gap-2">
						<Button variant="ghost" size="sm" onClick={() => setEditing(true)} dataTestId={`platform-${p.name}-registry-change`}>
							{t("registry.change")}
						</Button>
						<ConfirmButton
							label={t("registry.remove")}
							question={t("registry.confirmRemove", { platform: p.title })}
							confirmLabel={t("registry.remove")}
							cancelLabel={t("registry.cancel")}
							onConfirm={onRemove}
							disabled={busy}
							testId={`platform-${p.name}-registry-remove`}
						/>
					</div>
				</div>
			) : (
				<form className="flex flex-col gap-2" onSubmit={submit} noValidate data-testid={`platform-${p.name}-registry-form`}>
					<p className="text-muted-foreground text-xs">{t("registry.help")}</p>
					<label htmlFor={`${formId}-path`} className="text-xs font-medium">
						{t("registry.path")}
					</label>
					<Input
						id={`${formId}-path`}
						autoComplete="off"
						spellCheck={false}
						placeholder={t("registry.pathPlaceholder")}
						value={registry}
						onChange={(event) => setRegistry(event.target.value)}
						className="font-mono"
						data-testid={`platform-${p.name}-registry-path`}
					/>
					<label htmlFor={`${formId}-user`} className="text-xs font-medium">
						{t("registry.username")}
					</label>
					<Input
						id={`${formId}-user`}
						autoComplete="off"
						spellCheck={false}
						value={username}
						onChange={(event) => setUsername(event.target.value)}
						data-testid={`platform-${p.name}-registry-username`}
					/>
					<label htmlFor={`${formId}-token`} className="text-xs font-medium">
						{t("registry.token")}
					</label>
					<Input
						id={`${formId}-token`}
						type="password"
						autoComplete="off"
						spellCheck={false}
						placeholder={t("registry.tokenPlaceholder")}
						value={token}
						onChange={(event) => setToken(event.target.value)}
						className="font-mono"
						data-testid={`platform-${p.name}-registry-token`}
					/>
					<div className="flex justify-end gap-2">
						{saved && (
							<Button type="button" variant="ghost" size="sm" onClick={() => setEditing(false)}>
								{t("registry.cancel")}
							</Button>
						)}
						<Button
							type="submit"
							size="sm"
							isLoading={busy}
							disabled={hint !== undefined}
							aria-describedby={hint ? `${formId}-hint` : undefined}
							dataTestId={`platform-${p.name}-registry-save`}
						>
							{t("registry.save")}
						</Button>
					</div>
					{hint && (
						<p id={`${formId}-hint`} className="text-muted-foreground text-right text-sm" data-testid={`platform-${p.name}-registry-hint`}>
							{t(`registry.${hint}`)}
						</p>
					)}
				</form>
			)}
			{notice && (
				<p
					className="text-chart-success-ink flex items-center gap-1.5 text-xs"
					role="status"
					data-testid={`platform-${p.name}-registry-notice`}
				>
					<CheckCircle2 className="size-3.5" aria-hidden="true" />
					{notice}
				</p>
			)}
			{error && (
				<p role="alert" className="text-chart-error-ink text-xs break-words" data-testid={`platform-${p.name}-registry-error`}>
					{t("registry.error")}: {error}
				</p>
			)}
		</section>
	);
}

/** The registry block; without a store (a card rendered on its own) it shows the saved state only. */
export function PlatformRegistryBlock({ platform }: { platform: WorkerPlatform }) {
	const store = useContext(ReactReduxContext);
	if (!store) return <PlatformRegistryForm platform={platform} onSave={() => undefined} onRemove={() => undefined} />;
	return <PlatformRegistryLive platform={platform} />;
}

/** The registry block with its save and remove calls. */
function PlatformRegistryLive({ platform }: { platform: WorkerPlatform }) {
	const { t } = useTranslation("workerPlatforms");
	const [save, saveState] = useSavePlatformRegistryMutation();
	const [remove, removeState] = useRemovePlatformRegistryMutation();
	const [error, setError] = useState("");
	const [notice, setNotice] = useState("");
	const run = async (action: () => Promise<unknown>, done?: string) => {
		setError("");
		setNotice("");
		try {
			await action();
			if (done) setNotice(done);
		} catch (err) {
			setError(getBenchErrorMessage(err));
		}
	};
	return (
		<PlatformRegistryForm
			key={`${platform.registry?.registry ?? ""}-${platform.registry?.token_last4 ?? ""}`}
			platform={platform}
			busy={saveState.isLoading || removeState.isLoading}
			error={error || undefined}
			notice={notice || undefined}
			onSave={(registry, username, token) =>
				void run(() => save({ platform: platform.name, registry, username, token }).unwrap(), t("registry.saved", { registry }))
			}
			onRemove={() => void run(() => remove(platform.name).unwrap())}
		/>
	);
}