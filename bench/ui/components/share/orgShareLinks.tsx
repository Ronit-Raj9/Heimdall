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
				// Q-23: every column fits at 1440 (long ids are cut with the full id on hover, the
				// "On" column wraps); below sm each link stacks into a card with its Turn off button.
				<div className="min-w-0 rounded-lg border">
					<Table stack className="sm:table-fixed" data-testid="org-share-table">
						<TableHeader>
							<TableRow>
								<TableHead className="w-[22%]">{td("share.org.link")}</TableHead>
								<TableHead className="w-[24%]">{td("share.org.on")}</TableHead>
								<TableHead className="w-[14%]">{td("share.org.by")}</TableHead>
								<TableHead className="w-[11%]">{td("share.org.created")}</TableHead>
								<TableHead className="w-[11%]">{td("share.org.expiry")}</TableHead>
								<TableHead className="w-[8%]">{td("share.org.state")}</TableHead>
								<TableHead className="w-[10%]">
									<span className="sr-only">{t("revoke")}</span>
								</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{rows.map((s) => (
								<TableRow key={s.id} data-testid={`org-share-${s.id}`}>
									<TableCell className="font-medium" label={td("share.org.link")}>
										<span className="block min-w-0 truncate" title={s.label ? `${s.label} (${s.id})` : s.id}>
											{s.label || s.id}
										</span>
									</TableCell>
									<TableCell className="whitespace-normal" label={td("share.org.on")}>
										<span className="flex min-w-0 flex-col max-sm:items-end">
											<span>{t(`what.${s.kind}`)}</span>
											<span className="text-muted-foreground block max-w-full truncate font-mono text-xs" title={s.target}>
												{s.target}
											</span>
										</span>
									</TableCell>
									<TableCell label={td("share.org.by")}>
										<span className="block min-w-0 truncate">{creatorName(s) || td("share.org.unknownCreator")}</span>
									</TableCell>
									<TableCell className="tabular-nums" label={td("share.org.created")}>
										{formatDate(s.created_at)}
									</TableCell>
									<TableCell className="tabular-nums whitespace-normal" label={td("share.org.expiry")}>
										{s.expires_at ? formatDate(s.expires_at) : t("neverExpires")}
									</TableCell>
									<TableCell label={td("share.org.state")}>{t(`state.${s.state}`)}</TableCell>
									<TableCell className="text-right max-sm:justify-end">
										{s.state === "active" && (
											<Button
												type="button"
												variant="ghost"
												size="sm"
												className="max-sm:w-full"
												onClick={() => void turnOff(s)}
												dataTestId={`org-share-revoke-${s.id}`}
											>
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
