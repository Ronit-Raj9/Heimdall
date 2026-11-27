import { useConfirm } from "@/components/platform/confirmDialog";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { InlineError } from "@/components/runs/screenStates";
import { notify } from "@/lib/notify";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useListSharesQuery, useRevokeShareMutation, type Share } from "@/lib/store/apis/shareApi";
import i18n from "@/lib/i18n";
import "@/lib/types/shareI18n";
import "@/components/platform/ufintI18n";
import "./uf8bI18n";
import "./ufd4I18n";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { creatorName } from "./shareDialog";

function formatDate(iso?: string): string {
	if (!iso) return "";
	const d = new Date(iso);
	return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

/** Newest first, so a link just made is on top. Exported for tests. */
export function sortLinks(shares: Share[]): Share[] {
	return [...shares].sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? ""));
}

/**
 * Every share link of the organization (decision 7): who made it, when, on what, its expiry, and
 * Revoke with the destructive confirm. Admins only: the unfiltered GET /shares answers 403 to
 * anyone else, so for others this renders nothing and sends no request.
 */
export function OrgShareLinks() {
	const session = useSession();
	const isAdmin = canUse(session, "admin");
	const { t } = useTranslation("share");
	const { t: tu } = useTranslation("uf8b");
	const { t: td } = useTranslation("ufd4");
	const confirm = useConfirm();
	const links = useListSharesQuery(undefined, { skip: !isAdmin });
	const [revoke] = useRevokeShareMutation();
	const [revokeError, setRevokeError] = useState<unknown>(null);
	if (!isAdmin) return null;

	const turnOff = async (s: Share) => {
		const name = s.label || formatDate(s.created_at);
		const expiry = s.expires_at ? t("expires", { when: formatDate(s.expires_at) }) : tu("share.neverExpiresLower");
		if (
			!(await confirm(tu("share.revokeConfirmNamed", { name, expiry }), {
				destructive: true,
				title: i18n.t("confirm.revokeShare", { ns: "ufint", name }),
				confirmLabel: t("revoke"),
			}))
		)
			return;
		setRevokeError(null);
		try {
			await revoke(s.id).unwrap();
			notify({ text: t("revoked", { ns: "toast" }) });
		} catch (e) {
			setRevokeError(e);
		}
	};

	const rows = sortLinks(links.data?.shares ?? []);
	return (
		<section className="flex flex-col gap-3" data-testid="org-share-links">
			<div>
				<h2 className="text-base font-semibold">{td("share.org.title")}</h2>
				<p className="text-muted-foreground text-sm">{td("share.org.description")}</p>
			</div>
			{links.isError ? (
				<InlineError error={links.error} title={td("share.org.loadError")} testId="org-share-links-error" />
			) : links.isLoading ? null : rows.length === 0 ? (
				<p className="text-muted-foreground text-sm">{td("share.org.empty")}</p>
			) : (
				<div className="overflow-x-auto rounded-lg border">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>{td("share.org.link")}</TableHead>
								<TableHead>{td("share.org.on")}</TableHead>
								<TableHead>{td("share.org.by")}</TableHead>
								<TableHead>{td("share.org.created")}</TableHead>
								<TableHead>{td("share.org.expiry")}</TableHead>
								<TableHead>{td("share.org.state")}</TableHead>
								<TableHead />
							</TableRow>
						</TableHeader>
						<TableBody>
							{rows.map((s) => (
								<TableRow key={s.id} data-testid={`org-share-${s.id}`}>
									<TableCell className="font-medium">{s.label || s.id}</TableCell>
									<TableCell>
										{t(`what.${s.kind}`)} <span className="text-muted-foreground font-mono text-xs">{s.target}</span>
									</TableCell>
									<TableCell>{creatorName(s) || td("share.org.unknownCreator")}</TableCell>
									<TableCell className="tabular-nums">{formatDate(s.created_at)}</TableCell>
									<TableCell className="tabular-nums">{s.expires_at ? formatDate(s.expires_at) : t("neverExpires")}</TableCell>
									<TableCell>{t(`state.${s.state}`)}</TableCell>
									<TableCell>
										{s.state === "active" && (
											<Button type="button" variant="ghost" size="sm" onClick={() => void turnOff(s)} dataTestId={`org-share-revoke-${s.id}`}>
												{t("revoke")}
											</Button>
										)}
									</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				</div>
			)}
			<InlineError error={revokeError} title={tu("share.revokeFailed")} testId="org-share-revoke-error" />
		</section>
	);
}
