import { ArchiveRowMenu } from "@/components/ui/archiveAction";
import { ArchivedPill } from "@/components/ui/showArchivedChip";
import { isArchived } from "@/lib/store/apis/archiveApi";

/** A finding with the optional archive field (phase D decision 6). */
export type ArchivableFinding = { id: string; title?: string; archived_at?: string | null };

/**
 * A finding row's Archive or Unarchive menu (phase D decision 6), for the findings list owned by
 * another lane. The finding's state (open, expected, fixed) does not change: archive only hides it.
 */
export function FindingArchiveMenu({ finding, testId }: { finding: ArchivableFinding; testId?: string }) {
	return (
		<ArchiveRowMenu kind="finding" id={finding.id} name={finding.title || finding.id} archivedAt={finding.archived_at} testId={testId ?? `finding-${finding.id}`} />
	);
}

/** The "Archived" pill beside an archived finding's title; nothing for a finding that is not archived. */
export function FindingArchivedPill({ finding }: { finding: ArchivableFinding }) {
	return isArchived(finding) ? <ArchivedPill at={finding.archived_at} testId={`finding-archived-${finding.id}`} /> : null;
}
