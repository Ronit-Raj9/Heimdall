import { EmptyState } from "@/components/shell/emptyState";

// Web parity Phase 0 stub; its lane replaces it.
export default function ModelsView() {
	return (
		<div className="p-6">
			<EmptyState message="Coming soon" testId="models-coming-soon" />
		</div>
	);
}
