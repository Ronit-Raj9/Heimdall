import "./ufd5I18n";
import { Button } from "@/components/ui/button";
import { OverflowMenu, type OverflowMenuItem } from "@/components/agents/overflowMenu";
import { useConfirm, type ConfirmFn } from "@/components/platform/confirmDialog";
import { notify } from "@/lib/notify";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { ARCHIVE_ROLE, archiveApi, type ArchiveKind } from "@/lib/store/apis/archiveApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { Archive, ArchiveRestore } from "lucide-react";
import { useContext, useState, type ReactNode } from "react";
import { ReactReduxContext } from "react-redux";
import { useTranslation } from "react-i18next";

export interface ArchiveFlowDeps {
	confirm: ConfirmFn;
	archive: () => Promise<unknown>;
	unarchive: () => Promise<unknown>;
	notify: typeof notify;
	texts: { question: string; title: string; confirm: string; cancel: string; done: string };
}

/**
 * Asks once, archives, then shows a toast with Undo (unarchive). Returns the server's refusal
 * (for example on a running object) or null; "cancelled" when the person kept it. The answer is
 * read as optional: nothing assumes the object left any list.
 */
export async function archiveFlow(d: ArchiveFlowDeps): Promise<string | null | "cancelled"> {
	const ok = await d.confirm(d.texts.question, { title: d.texts.title, confirmLabel: d.texts.confirm, cancelLabel: d.texts.cancel });
	if (!ok) return "cancelled";
	try {
		await d.archive();
	} catch (e) {
		return getBenchErrorMessage(e) || "";
	}
	d.notify({ text: d.texts.done, undo: d.unarchive });
	return null;
}

/** Unarchives without a confirm (nothing is lost) and says so. Returns the refusal or null. */
export async function unarchiveFlow(unarchive: () => Promise<unknown>, done: string, n: typeof notify): Promise<string | null> {
	try {
		await unarchive();
	} catch (e) {
		return getBenchErrorMessage(e) || "";
	}
	n({ text: done });
	return null;
}

export interface UseArchiveArgs {
	kind: ArchiveKind;
	id: string;
	name: string;
	/** The row's archived_at; set means the item offers Unarchive. */
	archivedAt?: string | null;
	testId: string;
}

/**
 * The Archive (or Unarchive) item of a row menu or a detail page's More menu, shown only to the role
 * the server allows; `alert` shows the server's refusal. Put the items first, above any Delete.
 */
export function useArchiveItems({ kind, id, name, archivedAt, testId }: UseArchiveArgs): { items: OverflowMenuItem[]; alert: ReactNode } {
	const { t } = useTranslation("ufd5");
	const session = useSession();
	const confirm = useConfirm();
	// The store is read from context, not through the mutation hooks, so a screen rendered without
	// one (a static test render) simply shows no archive item.
	const store = useContext(ReactReduxContext)?.store;
	const [error, setError] = useState<string | null>(null);
	if (!store || !canUse(session, ARCHIVE_ROLE[kind]) || !id) return { items: [], alert: null };
	const dispatch = store.dispatch as (a: unknown) => { unwrap(): Promise<unknown> };
	const archive = (a: { kind: ArchiveKind; id: string }) => dispatch(archiveApi.endpoints.archiveObject.initiate(a));
	const unarchive = (a: { kind: ArchiveKind; id: string }) => dispatch(archiveApi.endpoints.unarchiveObject.initiate(a));
	const archived = typeof archivedAt === "string" && archivedAt !== "";
	const doUnarchive = () => unarchive({ kind, id }).unwrap();
	const runArchive = async () => {
		setError(null);
		const out = await archiveFlow({
			confirm,
			archive: () => archive({ kind, id }).unwrap(),
			unarchive: doUnarchive,
			notify,
			texts: { question: t("archive.body"), title: t("archive.title", { name }), confirm: t("archive.confirm"), cancel: t("archive.cancel"), done: t("archive.done", { name }) },
		});
		if (out !== null && out !== "cancelled") setError(out || t("archive.failed", { name }));
	};
	const runUnarchive = async () => {
		setError(null);
		const out = await unarchiveFlow(doUnarchive, t("archive.undone", { name }), notify);
		if (out !== null) setError(out || t("archive.unarchiveFailed", { name }));
	};
	const item: OverflowMenuItem = archived
		? { label: t("archive.unarchive"), icon: ArchiveRestore, onSelect: () => void runUnarchive(), testId: `${testId}-unarchive` }
		: { label: t("archive.action"), icon: Archive, onSelect: () => void runArchive(), testId: `${testId}-archive` };
	const alert = error ? (
		<p role="alert" className="text-danger-ink text-xs" data-testid={`${testId}-archive-error`}>
			{error}
		</p>
	) : null;
	return { items: [item], alert };
}

/**
 * A row's "..." menu: Archive or Unarchive first, then the extra items (a Delete goes below with
 * separatorBefore). Renders nothing when no item is left for this role.
 */
export function ArchiveRowMenu({ extra = [], menuLabel, ...args }: UseArchiveArgs & { extra?: OverflowMenuItem[]; menuLabel?: string }) {
	const { t } = useTranslation("ufd5");
	const { items, alert } = useArchiveItems(args);
	const all = [...items, ...extra];
	if (all.length === 0) return null;
	return (
		<div className="relative z-10 flex flex-col items-end gap-1">
			<OverflowMenu label={menuLabel ?? t("archive.rowMenu", { name: args.name })} items={all} testId={`${args.testId}-menu`} />
			{alert}
		</div>
	);
}

/**
 * Archive or Unarchive as a plain button, for a detail page whose More panel holds buttons (the
 * evaluation page): put it above Delete.
 */
export function ArchiveButton(args: UseArchiveArgs) {
	const { items, alert } = useArchiveItems(args);
	const item = items[0];
	if (!item) return null;
	const Icon = item.icon;
	return (
		<div className="flex flex-col gap-1">
			<Button size="sm" variant="outline" className="justify-start" onClick={item.onSelect} dataTestId={item.testId}>
				{Icon && <Icon aria-hidden="true" />}
				{item.label}
			</Button>
			{alert}
		</div>
	);
}
