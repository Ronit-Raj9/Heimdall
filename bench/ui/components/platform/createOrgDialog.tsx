import "@/components/platform/fw23I18n";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateOrgMutation, useSwitchOrgMutation } from "@/lib/store/apis/platformApi";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";

/** The org slug rule the server enforces (handlers/orgs.go slugPattern). */
const ORG_SLUG = /^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$/;

/** A short id for an organization name: lowercase letters and digits, other runs become one hyphen. */
export function slugFromName(name: string): string {
	return name
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, "-")
		.replace(/^-+|-+$/g, "")
		.slice(0, 40)
		.replace(/-+$/, "");
}

export interface CreateOrgDialogProps {
	onClose(): void;
}

/**
 * Create an organization: a name and a short id (filled from the name until edited). On success the
 * session switches to the new organization and the app reloads at Home, since every page belongs
 * to one organization.
 */
export function CreateOrgDialog({ onClose }: CreateOrgDialogProps) {
	const { t } = useTranslation("fw23");
	const [createOrg, createState] = useCreateOrgMutation();
	const [switchOrg, switchState] = useSwitchOrgMutation();
	const [name, setName] = useState("");
	const [slug, setSlug] = useState("");
	const [slugEdited, setSlugEdited] = useState(false);
	const effectiveSlug = slugEdited ? slug.trim() : slugFromName(name);
	const slugOk = ORG_SLUG.test(effectiveSlug);
	const busy = createState.isLoading || switchState.isLoading;
	const error = createState.error ?? switchState.error;

	const nameRef = useRef<HTMLInputElement>(null);

	const submit = async () => {
		if (!name.trim() || !slugOk || busy) return;
		try {
			const org = await createOrg({ name, slug: effectiveSlug }).unwrap();
			await switchOrg({ org: org.id }).unwrap();
			window.location.assign("/");
		} catch {
			// The error state renders the failure.
		}
	};

	return (
		// The shared Dialog (Q-31): focus trapped inside, Escape closes, focus returns to the opener.
		<Dialog
			open
			onOpenChange={(open) => !open && onClose()}
			size="sm"
			closeLabel={t("orgs.cancel")}
			hideClose
			initialFocus={nameRef}
			title={t("orgs.createTitle")}
			description={t("orgs.createIntro")}
			testId="create-org-dialog"
		>
			<form
				className="space-y-4 text-left"
				onSubmit={(e) => {
					e.preventDefault();
					void submit();
				}}
			>
				<label className="flex flex-col gap-1 text-xs">
					<span className="text-muted-foreground">{t("orgs.name")}</span>
					<Input
						value={name}
						onChange={(e) => setName(e.target.value)}
						placeholder={t("orgs.namePlaceholder")}
						maxLength={120}
						ref={nameRef}
						data-testid="create-org-name"
					/>
				</label>
				<label className="flex flex-col gap-1 text-xs">
					<span className="text-muted-foreground">{t("orgs.slug")}</span>
					<Input
						value={effectiveSlug}
						onChange={(e) => {
							setSlugEdited(true);
							setSlug(e.target.value.toLowerCase());
						}}
						maxLength={40}
						aria-invalid={effectiveSlug !== "" && !slugOk}
						className="font-mono"
						data-testid="create-org-slug"
					/>
					<span className="text-muted-foreground">{t("orgs.slugHint")}</span>
				</label>
				{effectiveSlug !== "" && !slugOk && (
					<p role="alert" className="text-chart-error-ink text-xs" data-testid="create-org-problem">
						{t("orgs.badSlug")}
					</p>
				)}
				{error && (
					<p role="alert" className="text-chart-error-ink text-xs" data-testid="create-org-error">
						{t("orgs.createError", { error: getBenchErrorMessage(error) })}
					</p>
				)}
				<div className="flex justify-end gap-2">
					<Button type="button" variant="ghost" size="sm" onClick={onClose} dataTestId="create-org-cancel">
						{t("orgs.cancel")}
					</Button>
					<Button type="submit" size="sm" disabled={!name.trim() || !slugOk} isLoading={busy} dataTestId="create-org-submit">
						{t("orgs.submit")}
					</Button>
				</div>
			</form>
		</Dialog>
	);
}